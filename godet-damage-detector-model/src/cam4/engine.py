"""Side-plate engine (one per camera: CAM-4, CAM-3): its own sequencing lane, stream -> identity -> history, evidence frames,
Tier-2 state/events and health.

The server routes each side-plate camera's requests to its engine (CAM-1 keeps its own lane and pipeline). Framework
light: the gRPC message classes are passed in (pb2), so the engine is testable without a server.

Evidence frames. A fault spot is only known once the detector has scored its columns, about
10 s after the frame (it needs chain on both sides). The engine therefore keeps recent JPEGs,
one per 4 px of chain position (a stopped conveyor does not fill the buffer), and when a
strong spot is scored it hands that frame back in a Tier-1 response (`retained_frames`). The
pipeline keeps it; a later alert names it by frame_id. A godet's evidence is its latest strong
pass that has a retained frame, so the frame is recent enough to still be in the pipeline cache.

Godet ids: the chain map numbers godets 0..1209 (the review pages use these numbers). The
system treats 0 as "no godet", so godet 0 is reported as 1210 (the same godet one loop on).
"""
from __future__ import annotations

import logging
import threading
import time
from collections import OrderedDict, deque

import numpy as np

from .bundle import load_cam4_bundle
from .identity import Cam4History, Cam4Identity
from .stream import ROW0, Cam4Stream

LOG = logging.getLogger("clinker-vision-model.cam4")

REORDER_WINDOW = 64
SNAP_EVERY_PASSES = 200
EVIDENCE_HALF = 130          # evidence box half size (px) around the fault spot
POPULATION_ALARM = 0.15      # > 15 % of godets confirmed: the view, not the chain, is wrong

RING_BUCKET = 4.0            # keep one frame per 4 px of chain position
RING_MAX = 900               # hard cap on buffered JPEGs (~0.5 GB worst case at 576 KB)
RING_KEEP_BEHIND = 200       # px behind the scored columns still kept (evidence reach)
RETAIN_BLOCK = 80            # columns per retention block (half a plate)
RETAIN_REACH = 120           # a retained frame serves spots within this many columns
RETAIN_FACTOR = 0.75         # retain at 75 % of the pass alarm (relative), generous on purpose
RETAIN_MIN_BLOCKS = 200      # block peaks needed before the retention threshold is trusted
RETAINED_MAX = 4000          # retained-frame index kept for evidence lookup (no JPEG bytes)
OUTBOX_PER_RESPONSE = 2      # retained JPEGs attached per Tier-1 response (message size)


class Cam4Engine:
    def __init__(self, bundle_dir, store=None):
        self.b = load_cam4_bundle(bundle_dir)
        self.camera_id = self.b.camera_id
        # CAM-4 keeps its original store key so a running server keeps its history.
        self.state_key = "cam4_state" if self.camera_id == "CAM-4" else f"sideplate_state_{self.camera_id}"
        self.version = self.b.version
        self.n = int(self.b.ident["godets"])
        self.hist = Cam4History(self.b.rules, self.n)
        self.store = store
        self.lock = threading.Lock()
        self.expected = None
        self.hold = {}
        self.counters = {"frames_total": 0, "dead_letters_total": 0, "sequence_gaps_total": 0,
                         "late_frames_total": 0, "deadline_exceeded_total": 0,
                         "sequence_restarts_total": 0, "retained_frames_total": 0,
                         "evidence_missing_total": 0}
        self._passes_since_snap = 0
        self.ever_locked = False
        # evidence
        self.ring = OrderedDict()           # bucket -> frame record (with JPEG)
        self.retained = OrderedDict()       # frame_id -> frame record (without JPEG)
        self.outbox = deque()               # frame records (with JPEG) to hand back
        self.block_peaks = deque(maxlen=2 * self.n)
        self.pass_alarm = float(self.b.rules["pass_alarm_relative"])
        self._build_front_end(None, None)
        if store is not None:
            snap = store.load_kv(self.state_key)
            if snap is not None and snap.get("version") == self.version:
                self.hist.restore(snap["history"])
                self._build_front_end(snap["stream"], snap["identity"])
                LOG.info("%s state restored: %d godets with history", self.camera_id, len(self.hist.g))
            elif snap is not None:
                LOG.warning("%s stored state is for bundle %s, not %s: starting fresh history", self.camera_id,
                            snap.get("version"), self.version)
        LOG.info("%s bundle loaded version=%s godets=%d", self.camera_id, self.version, self.n)

    def _build_front_end(self, stream_snap, ident_snap):
        """(Re)build odometer + identity. History is kept; identity re-locks on the map."""
        self.stream = Cam4Stream(self.b)
        self.ident = Cam4Identity(self.b)
        if stream_snap:
            self.stream.restore(stream_snap)
        if ident_snap:
            self.ident.restore(ident_snap)
        self.stream.subscribe(self.ident.on_columns)
        self.stream.subscribe(self._on_columns)
        self.ident.subscribe(self._on_pass)
        self.ring.clear()

    # ---- sequencing lane (same policy as CAM-1: reorder window 64, gaps declared) -----
    def sequence(self, req, release):
        """Feed one request; release(req) is called for every frame now in order."""
        seq = req.sequence_no
        if self.expected is not None and seq + REORDER_WINDOW < self.expected:
            self._restart(seq)
        if self.expected is None:
            self.expected = seq
        if seq < self.expected:
            self._bump("late_frames_total")
            return
        if seq == self.expected:
            release(req); self.expected += 1
            while self.expected in self.hold:
                release(self.hold.pop(self.expected)); self.expected += 1
            return
        self.hold[seq] = req
        if len(self.hold) > REORDER_WINDOW:
            missing_to = min(self.hold)
            n = missing_to - self.expected
            self.stream.on_gap(n)
            self._bump("sequence_gaps_total")
            LOG.warning("%s sequence gap seq=%d..%d (%d frames)", self.camera_id, self.expected, missing_to - 1, n)
            self.expected = missing_to
            while self.expected in self.hold:
                release(self.hold.pop(self.expected)); self.expected += 1

    def _restart(self, seq):
        """The sender restarted (sequence_no fell back): the chain moved an unknown amount,
        so rebuild the odometer and re-lock identity. Godet history and loop count survive."""
        LOG.warning("%s sequence restart: seq %d after expected %d -> re-locking on the chain map", self.camera_id,
                    seq, self.expected)
        self._bump("sequence_restarts_total")
        with self.lock:
            s_snap, i_snap = self.stream.snapshot(), self.ident.snapshot()
        self._build_front_end(s_snap, i_snap)
        self.hold.clear()
        self.expected = None

    def _bump(self, k):
        with self.lock:
            self.counters[k] += 1

    # ---- frames ------------------------------------------------------------------------------
    def on_frame(self, jpeg, frame_id, sequence_no, captured_at=None):
        """captured_at: google.protobuf.Timestamp or None (kept with retained evidence)."""
        d, ok = self.stream.on_frame(jpeg, frame_id, sequence_no)
        if ok:
            self._bump("frames_total")
            self.ever_locked |= self.ident.locked
            self._remember(jpeg, frame_id, sequence_no, captured_at, d["chain_pos"])
        return d, ok

    def _remember(self, jpeg, frame_id, sequence_no, captured_at, s):
        key = int(s // RING_BUCKET)
        self.ring.pop(key, None)
        self.ring[key] = {"frame_id": frame_id, "sequence_no": int(sequence_no or 0),
                          "captured_at": captured_at, "s": float(s), "jpeg": jpeg}
        oldest_needed = self.stream.done - self.b.pad - RING_KEEP_BEHIND
        while self.ring:
            k, rec = next(iter(self.ring.items()))
            if rec["s"] < oldest_needed or len(self.ring) > RING_MAX:
                self.ring.popitem(last=False)
            else:
                break

    def _nearest(self, records, col):
        """Record whose slit sat closest to this chain column (and within reach)."""
        target = col - self.b.pad
        best = None
        for rec in records:
            d = abs(rec["s"] - target)
            if d <= RETAIN_REACH and (best is None or d < abs(best["s"] - target)):
                best = rec
        return best

    def _on_columns(self, cols, fp_raw, sev, top, missing):
        """Scored columns: retain the frame of every strong half-plate block."""
        for i0 in range(0, len(cols), RETAIN_BLOCK):
            s = np.where(missing[i0:i0 + RETAIN_BLOCK], -np.inf, sev[i0:i0 + RETAIN_BLOCK])
            if not np.isfinite(s).any():
                continue
            k = int(np.argmax(s))
            peak = float(s[k])
            self.block_peaks.append(peak)
            if len(self.block_peaks) < RETAIN_MIN_BLOCKS:
                continue
            p = np.fromiter(self.block_peaks, float)
            med, p90 = np.median(p), np.percentile(p, 90)
            if peak < med + RETAIN_FACTOR * self.pass_alarm * max(p90 - med, 1e-3):
                continue
            rec = self._nearest(self.ring.values(), int(cols[i0 + k]))
            if rec is None or rec["frame_id"] in self.retained:
                continue
            light = {k2: v for k2, v in rec.items() if k2 != "jpeg"}
            with self.lock:
                self.retained[rec["frame_id"]] = light
                while len(self.retained) > RETAINED_MAX:
                    self.retained.popitem(last=False)
                self.outbox.append(rec)
                self.counters["retained_frames_total"] += 1

    def take_retained(self, n=OUTBOX_PER_RESPONSE):
        with self.lock:
            out = []
            while self.outbox and len(out) < n:
                out.append(self.outbox.popleft())
            return out

    def external_id(self, gid):
        return int(gid) if gid else self.n

    # ---- passes ------------------------------------------------------------------------------
    def _on_pass(self, rec):
        rec.godet_id = self.external_id(rec.godet_id)
        col = rec.best_col
        with self.lock:
            frame = self._nearest(reversed(list(self.retained.values())[-400:]), col)
        evidence = None
        if frame is not None:
            a = frame["s"] - (col - self.b.pad)              # along travel, in the frame
            bb = rec.top_row + ROW0 + 25 - self.b.slit_half  # across the band (fault spot)
            x, y = self.b.p0 + a * self.b.u + bb * self.b.v
            W, H = self.b.frame_size
            x0, y0 = max(0.0, x - EVIDENCE_HALF), max(0.0, y - EVIDENCE_HALF)
            x1, y1 = min(W, x + EVIDENCE_HALF), min(H, y + EVIDENCE_HALF)
            ts = frame["captured_at"]
            evidence = {"frame_id": frame["frame_id"], "sequence_no": frame["sequence_no"],
                        "captured_at": None if ts is None else [int(ts.seconds), int(ts.nanos)],
                        "box": [x0 / W, y0 / H, (x1 - x0) / W, (y1 - y0) / H], "loop": rec.loop}
            rec.sequence_no = frame["sequence_no"]
        with self.lock:
            self.hist.on_pass(rec, evidence)
            while self.hist.new_alerts:
                a = self.hist.new_alerts.popleft()
                LOG.warning("%s godet %d CONFIRMED overlap fault (severity %.2f, loop %d)", self.camera_id,
                            a["godet_id"], a["severity"], a["loop"])
                if not self.hist.g[a["godet_id"]].get("evidence"):
                    self.counters["evidence_missing_total"] += 1
        self._passes_since_snap += 1
        if self._passes_since_snap >= SNAP_EVERY_PASSES:
            self._passes_since_snap = 0
            self.persist()

    # ---- persistence ------------------------------------------------------------------------
    def persist(self):
        if self.store is None:
            return
        try:
            with self.lock:
                snap = {"version": self.version, "history": self.hist.snapshot(),
                        "identity": self.ident.snapshot(), "stream": self.stream.snapshot(),
                        "saved_at": time.time()}
            self.store.save_kv(self.state_key, snap)
        except Exception as e:                                   # never take the stream down
            LOG.error("%s snapshot failed: %s", self.camera_id, e)

    # ---- Tier 2 ---------------------------------------------------------------------------------
    def godet_state(self, request, pb2):
        r = pb2.GodetStateResponse()
        r.model_version = self.version
        r.camera_id = self.camera_id
        with self.lock:
            G = {k: {"rel": list(v["rel"]), "loops": list(v["loops"]), "state": v["state"],
                     "evidence": v["evidence"], "confirmed_loop": v.get("confirmed_loop")}
                 for k, v in self.hist.g.items()}
        requested = {int(g) for g in request.godet_ids}
        for g in sorted(G):
            v = G[g]
            if v["state"] != "confirmed" or v["confirmed_loop"] is None:
                continue
            if requested and g not in requested:
                continue
            ev = r.events.add()
            ev.event_key = f"{self.camera_id}:DAMAGE:{g}:{v['confirmed_loop']}"
            ev.kind = "damage"
            ev.godet_id = g
            ev.loop_no = int(v["confirmed_loop"])
            ev.state = "confirmed"
            ev.measurements["severity"] = float(np.median(v["rel"]))
            ev.measurements["passes_seen"] = float(len(v["rel"]))
            e = v["evidence"]
            if e and e.get("frame_id"):
                ev.evidence_frame_id = e["frame_id"]
                bx = e["box"]
                ev.evidence_box.x, ev.evidence_box.y = bx[0], bx[1]
                ev.evidence_box.width, ev.evidence_box.height = bx[2], bx[3]
                ev.measurements["evidence_loop"] = float(e.get("loop", -1))
                if e.get("captured_at"):
                    ev.occurred_at.seconds, ev.occurred_at.nanos = e["captured_at"]
        wanted = sorted(requested) or sorted(
            g for g, v in G.items() if v["state"] in ("confirmed", "suspect"))
        for g in wanted:
            gr = r.godets.add()
            gr.godet_id = g
            v = G.get(g)
            if v is None:
                gr.state = "healthy"
                continue
            gr.state = v["state"]
            gr.severity = float(np.median(v["rel"])) if v["rel"] else 0.0
            gr.passes_seen = len(v["rel"])
            gr.last_seen_loop = int(v["loops"][-1]) if v["loops"] else 0
            if request.include_history:
                gr.severity_history.extend(float(x) for x in v["rel"])
        self.fill_health(r.health)
        return r

    def fill_health(self, h):
        s = self.stream
        quals = list(s.quals)
        lines = []
        if s.chain_status() == 2:
            lines.append("conveyor stopped")
        if not self.hist.ready():
            lines.append(f"history building: {len(self.hist.pop)} passes of 300 needed")
        view_lost = len(quals) >= 100 and float(np.median(quals)) < 0.25
        share = self.hist.confirmed_share()
        if view_lost:
            status = "template_lost"
            lines.insert(0, "VIEW LOST: chain movement unreadable (camera moved, blocked or too dark?)")
        elif not self.ident.locked and (self.ident.resyncing or self.ever_locked):
            status = "resyncing"
            lines.insert(0, "lost the chain map: re-locking")
        elif not self.ident.locked:
            status = "not_ready"
            lines.insert(0, "locking onto the chain map (needs about 1 minute of moving chain)")
        elif self.hist.ready() and len(self.hist.g) >= 300 and share > POPULATION_ALARM:
            status = "population_alarm"
            lines.insert(0, f"POPULATION: {share:.0%} of godets confirmed - check lighting/view before trusting alerts")
        else:
            status = "ok"
        h.ready = bool(self.ident.locked and self.hist.ready())
        h.loop_locked = bool(self.ident.locked)
        h.status = status
        h.detail = "; ".join(lines) if lines else "all clear"
        with self.lock:
            items = dict(self.counters)
            items["evidence_buffer_frames"] = len(self.ring)
            items["evidence_outbox_frames"] = len(self.outbox)
        items.update({f"stream_{k}": v for k, v in s.counters.items()})
        items.update({f"identity_{k}": v for k, v in self.ident.counters.items()})
        items["godets_confirmed"] = sum(v["state"] == "confirmed" for v in self.hist.g.values())
        items["godets_suspect"] = sum(v["state"] == "suspect" for v in self.hist.g.values())
        items["godets_seen"] = len(self.hist.g)
        for k, v in items.items():
            h.counters[k] = float(v)
