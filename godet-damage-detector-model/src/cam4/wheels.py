"""Wheel (galet) tracker for a side-plate camera: wheels -> godet map -> distribution rules.

Streaming port of the calibration repo's cam4/wheels.py + cam4/wheelmap.py (WHEELS_PLAN.md).

Per scored column the stream says whether the rail stripe is hidden and whether the strip is
readable. A wheel is a hidden stretch at least `min_width` columns wide, fully readable (pieces
split by a bright hub are joined; two touching wheels are split by the typical width). Each wheel
is put on the chain map through identity; each chain loop is one pass.

After every completed pass once 3 passes of the current identity lock exist (placements of
different locks can differ locally by ~1 godet; until then the earlier flags stand):
  reference = wheels seen on >= 2 passes (1 where only one pass could see the spot) and on at
              least half of the passes that could see them
  rule A    = 5 or more consecutive godets with no reference wheel         -> wheel_gap
  rule B    = 2 or more reference wheels within any 3 consecutive godets   -> wheel_density
  rule M    = a reference wheel absent on the 2 latest passes that could see it -> wheel_missing
A wheel belongs to the godet containing its centre (agreed with the user 2026-09-27).

A and B describe how the chain is built, so each place is raised once with a stable key; M is a
change. Evidence is a picture of the unrolled chain around the place (like the review page).
"""
from __future__ import annotations

import threading
import time
import uuid
from collections import OrderedDict, deque

import cv2
import numpy as np

DEFAULTS = {"min_width": 90, "join_gap": 40, "typical_width": 130}
MATCH = 60                  # same wheel on another pass (within one lock: offsets ~7 columns)
KEEP_PASSES = 8
MIN_PASSES = 3              # 2026-09-07 replay: judged on 1 or 2 passes, one missed or doubled wheel raised
                            # a flag that the next loop cleared (13 of 15 short-lived flags); offline: 3-4
GAP_GODETS = 5
DENSE_WINDOW, DENSE_MIN = 3, 2
COVER_RES = 8               # coverage bitmap resolution (map columns per cell)
CHUNK = 2000                # strip columns per stored evidence chunk
CONTEXT_GODETS = 3
REG_WINDOW = 6 * 161        # registration: local wheel pattern each side of a wheel (columns)
REG_SHIFTS = np.arange(-320, 321, 4)
REG_TOL = 40
FLAG_TOL = 2                # a place found again within +-2 godets keeps its flag (and key)
SCALE_TOL = 0.04            # a stretch between two anchors must map ~1:1; otherwise one of them is wrong


def circ(d, L):
    return (d + L / 2) % L - L / 2


def local_shift(base, other, x, L):
    """Shift (columns) that best lines up `other`'s wheels with `base`'s around map position x
    (calibration repo cam4/wheelmap.py). Live placements of two passes can differ locally."""
    b = base[np.abs(circ(base - x, L)) <= REG_WINDOW]
    o = other[np.abs(circ(other - x, L)) <= REG_WINDOW + 320]
    if len(b) == 0 or len(o) == 0:
        return 0.0
    d = circ(o[None, :] - b[:, None], L)
    score = np.array([(np.abs(d - s) <= REG_TOL).any(1).sum() for s in REG_SHIFTS])
    ties = REG_SHIFTS[score == score.max()]
    return float(ties[np.argmin(np.abs(ties))])        # prefer the smallest shift


class WheelTracker:
    def __init__(self, bundle, ident, camera_id, evidence_sink=None):
        self.cfg = dict(DEFAULTS, **(getattr(bundle, "wheels", None) or {}))
        self.ident = ident
        self.cam = camera_id
        self.L = int(bundle.ident["loop_cols"]); self.P = float(bundle.ident["pitch"])
        self.phase = float(bundle.ident["phase"]); self.N = int(bundle.ident["godets"])
        self.evidence_sink = evidence_sink
        self.run = None                    # [start, end] of the open hidden run (absolute columns)
        self.run_ok = True
        self.waiting = deque()             # wheel centre columns not yet bracketed by anchors
        self.cover_wait = deque()          # readable columns (subsampled) not yet bracketed by anchors
        self.passes = OrderedDict()        # loop -> {"wheels": [(pos, col)], "cover": bool[L/COVER_RES]}
        self.lock_id = None
        self.evaluated = -1
        self.flags = {}                    # key -> flag dict (active and past)
        self.flags_lock = threading.Lock()  # stream thread writes, Tier-2 polls read
        self.chunks = deque()              # (col0, jpeg, cols, pos)
        self._chunk = None
        self.counters = {"wheels_total": 0, "wheel_passes_total": 0, "wheel_evaluations_total": 0,
                         "wheels_unplaced_total": 0}
        self.trace = None                  # replay/diagnostics: set to a list to record every evaluation

    # ---- identity helpers ---------------------------------------------------------------------
    def _positions(self, cols):
        """Unwrapped map position of columns (None if identity is not locked)."""
        idn = self.ident
        if not idn.locked or not idn.anchors:
            return None
        c_last, p_last = idn.anchors[-1]
        scale = 1.0
        if len(idn.anchors) >= 3:
            cs = np.array([a[0] for a in idn.anchors], float); ps = np.array([a[1] for a in idn.anchors], float)
            if cs[-1] - cs[0] >= 15000:
                scale = float(np.clip(np.polyfit(cs, ps, 1)[0], 0.98, 1.02))
        return p_last + (np.asarray(cols, float) - c_last) * scale

    def _interpolated(self, cols):
        """Map positions of columns between the two anchors around each (as the offline map does),
        and whether that stretch is trustworthy. Only columns at or before the last anchor.
        - Extrapolating past the last anchor was off by 50-100 columns over hundreds of godets after
          a conveyor stop (2026-09-07 replay): the same wheel became two on different passes.
        - A wrong anchor squeezes the stretch next to it (after the noon re-lock, ~16 godets of chain
          landed on 2): a stretch whose map/strip ratio is off by more than SCALE_TOL is "not seen"."""
        an = np.array(self.ident.anchors, float)
        cols = np.asarray(cols, float)
        if len(an) < 2:
            return cols + (an[0, 1] - an[0, 0]), np.ones(len(cols), bool)
        k = np.clip(np.searchsorted(an[:, 0], cols), 1, len(an) - 1)
        c0, p0, c1, p1 = an[k - 1, 0], an[k - 1, 1], an[k, 0], an[k, 1]
        scale = (p1 - p0) / np.maximum(c1 - c0, 1.0)
        old = cols < an[0, 0]                          # older than every anchor kept: 1:1 from the first
        scale[old] = 1.0
        pos = np.where(old, an[0, 1] + (cols - an[0, 0]), p0 + (cols - c0) * scale)
        return pos, np.abs(scale - 1.0) <= SCALE_TOL

    def _place_waiting(self):
        idn = self.ident
        if not idn.locked or not idn.anchors:
            return
        last = idn.anchors[-1][0]
        n = 0
        while n < len(self.waiting) and self.waiting[n] <= last:
            n += 1
        if n:
            cols = [self.waiting.popleft() for _ in range(n)]
            pos, ok = self._interpolated(cols)
            for c, p, good in zip(cols, pos, ok):
                if not good:
                    self.counters["wheels_unplaced_total"] += 1
                    continue
                self._pass(self._loop(p))["wheels"].append((float(p % self.L), int(c)))
                self.counters["wheels_total"] += 1
        while self.cover_wait and self.cover_wait[0][-1] <= last:
            cols = self.cover_wait.popleft()
            pos, ok = self._interpolated(cols)
            for p in pos[ok]:
                self._pass(self._loop(p))["cover"][int((p - self.phase) % self.L) // COVER_RES] = True

    def _loop(self, pos):
        idn = self.ident
        return int(max(np.floor((pos - self.phase) / self.L) - idn.loop_base, 0) + idn.loop_offset)

    def godet(self, pos):
        return int(((pos - self.phase) % self.L) // self.P) % self.N

    def ext(self, g):
        return int(g) if g else self.N                 # godet 0 is reported as 1210

    # ---- input from the stream ----------------------------------------------------------------
    def on_wheels(self, cols, hidden, readable, strip):
        if len(cols) == 0:
            return
        lock = self.ident.counters.get("locks_total", 0)
        if lock != self.lock_id:                       # new lock: placements may differ, start over
            self.lock_id = lock
            self.passes.clear(); self.evaluated = -1; self.chunks.clear(); self._chunk = None
            self.waiting.clear(); self.cover_wait.clear()
        pos = self._positions(cols)
        self._store_strip(cols, strip, pos)
        if pos is not None:                            # coverage of this pass, placed like the wheels
            rc = np.asarray(cols)[readable][::COVER_RES // 2]
            if len(rc):
                self.cover_wait.append(rc)
        self._place_waiting()                          # anchors may have moved on since the last batch
        # hidden runs, carried across batches
        for c, h, r in zip(cols.tolist(), hidden.tolist(), readable.tolist()):
            if h:
                if self.run is not None and c - self.run[1] <= self.cfg["join_gap"]:
                    self.run[1] = c + 1
                    self.run_ok &= r
                else:
                    self._close_run()
                    self.run, self.run_ok = [c, c + 1], r
            elif self.run is not None:
                if c - self.run[1] > self.cfg["join_gap"]:
                    self._close_run()
                else:
                    self.run_ok &= r                   # the gap inside a joined wheel must be readable too
        if pos is not None and self.ident.anchors:     # every wheel before the last anchor is placed
            self._maybe_evaluate(self._loop(self.ident.anchors[-1][1]))

    def _close_run(self):
        run, ok, self.run = self.run, self.run_ok, None
        if run is None or not ok:
            return
        a, b = run
        w = b - a
        if w < self.cfg["min_width"]:
            return
        n = max(1, int(round(w / self.cfg["typical_width"]))) if w > 1.6 * self.cfg["typical_width"] else 1
        for k in range(n):
            col = a + (2 * k + 1) * w // (2 * n)
            if self.ident.locked:
                self.waiting.append(col)               # placed once an anchor passes it
        self._place_waiting()

    def _pass(self, lp):
        lp = int(lp)                                   # plain int: the state is saved as JSON
        if lp not in self.passes:
            self.passes[lp] = {"wheels": [], "cover": np.zeros(self.L // COVER_RES + 1, bool)}
            while len(self.passes) > KEEP_PASSES + 1:
                self.passes.popitem(last=False)
        return self.passes[lp]

    # ---- evidence strip ----------------------------------------------------------------------
    def _store_strip(self, cols, strip, pos):
        if pos is None:
            return
        if self._chunk is None:
            self._chunk = {"cols": [], "img": [], "pos": []}
        ch = self._chunk
        ch["cols"].append(np.asarray(cols)); ch["img"].append(strip); ch["pos"].append(np.asarray(pos) % self.L)
        if sum(len(c) for c in ch["cols"]) >= CHUNK:
            img = np.concatenate(ch["img"], 1)
            ok, buf = cv2.imencode(".jpg", img, [cv2.IMWRITE_JPEG_QUALITY, 80])
            c = np.concatenate(ch["cols"]); p = np.concatenate(ch["pos"])
            if ok:
                self.chunks.append((int(c[0]), buf.tobytes(), c[::10].copy(), p[::10].copy()))
            self._chunk = None
            limit = int(1.2 * self.L / CHUNK) + 2
            while len(self.chunks) > limit:
                self.chunks.popleft()

    def _evidence(self, first, last, wheels_by_col):
        """JPEG of the unrolled chain around godets first..last (latest pass) + the flagged box."""
        x0 = (self.phase + (first - CONTEXT_GODETS) * self.P) % self.L
        span = (((last - first) % self.N) + 1 + 2 * CONTEXT_GODETS) * self.P
        parts, colmap = [], []
        for col0, jpeg, cs, ps in self.chunks:
            inside = np.abs(circ(ps - (x0 + span / 2), self.L)) <= span / 2
            if inside.any():
                parts.append((col0, jpeg, cs, ps, inside))
        if not parts:
            return None
        c_lo = min(int(cs[ins].min()) for _, _, cs, _, ins in parts)
        c_hi = max(int(cs[ins].max()) for _, _, cs, _, ins in parts) + 10
        imgs = []
        for col0, jpeg, cs, ps, ins in parts:
            img = cv2.imdecode(np.frombuffer(jpeg, np.uint8), cv2.IMREAD_GRAYSCALE)
            a, b = max(0, c_lo - col0), min(img.shape[1], c_hi - col0)
            if b > a:
                imgs.append(img[:, a:b])
        if not imgs:
            return None
        tile = cv2.cvtColor(np.concatenate(imgs, 1), cv2.COLOR_GRAY2BGR)
        allc = np.concatenate([cs for _, _, cs, _, _ in parts]); allp = np.concatenate([ps for _, _, _, ps, _ in parts])
        o = np.argsort(allc); allc, allp = allc[o], allp[o]

        def col_of(x):
            k = int(np.argmin(np.abs(circ(allp - x, self.L))))
            return int(allc[k]) - c_lo
        H, W = tile.shape[:2]
        for k in range(((last - first) % self.N) + 2 + 2 * CONTEXT_GODETS):
            g = (first - CONTEXT_GODETS + k) % self.N
            x = col_of((self.phase + g * self.P) % self.L)
            cv2.line(tile, (x, 0), (x, 20), (255, 255, 255), 2)
            cv2.putText(tile, str(self.ext(g)), (x + 5, 16), 0, 0.5, (255, 255, 255), 1)
        for c in wheels_by_col:
            if c_lo <= c < c_hi:
                cv2.rectangle(tile, (c - c_lo - 60, H - 70), (c - c_lo + 60, H - 25), (0, 0, 255), 2)
        xa = col_of((self.phase + first * self.P) % self.L)
        xb = col_of((self.phase + (last + 1) * self.P) % self.L)
        xa, xb = sorted((max(0, xa), min(W - 1, xb)))
        ok, buf = cv2.imencode(".jpg", tile, [cv2.IMWRITE_JPEG_QUALITY, 88])
        if not ok or xb <= xa:
            return None
        return buf.tobytes(), [xa / W, 0.0, (xb - xa) / W, 1.0]

    # ---- rules -------------------------------------------------------------------------------
    def _maybe_evaluate(self, current_loop):
        done = [lp for lp in self.passes if lp < current_loop]
        if not done or max(done) <= self.evaluated:
            return
        self.evaluated = max(done)
        self.counters["wheel_passes_total"] += 1
        passes = [self.passes[lp] for lp in sorted(done)][-KEEP_PASSES:]
        if len(passes) < MIN_PASSES:
            return                                     # flags neither raised nor cleared until then
        self.counters["wheel_evaluations_total"] += 1
        self._evaluate(passes, self.evaluated)

    def _seen(self, p, x):
        return bool(p["cover"][int(x) // COVER_RES])

    def _registered(self, passes):
        """Copies of the passes with wheels expressed in the most readable pass's coordinates."""
        L = self.L
        base = max(passes, key=lambda p: p["cover"].mean())
        bx = np.array([w[0] for w in base["wheels"]], float)
        out = []
        for p in passes:
            if p is base or not p["wheels"]:
                out.append(p); continue
            x = np.array([w[0] for w in p["wheels"]], float)
            sh = np.array([local_shift(bx, x, v, L) for v in x])
            out.append({"cover": p["cover"], "wheels": [(float((v - d) % L), w[1]) for v, d, w in zip(x, sh, p["wheels"])]})
        return out

    def _reference(self, passes):
        L = self.L
        allx = np.sort(np.concatenate([[w[0] for w in p["wheels"]] for p in passes if p["wheels"]] or [[]]))
        if len(allx) == 0:
            return []
        groups, cur = [], [allx[0]]
        for v in allx[1:]:
            if v - cur[-1] <= MATCH:
                cur.append(v)
            else:
                groups.append(cur); cur = [v]
        groups.append(cur)
        if len(groups) > 1 and (groups[0][0] + L) - groups[-1][-1] <= MATCH:
            groups[0] = groups.pop() + groups[0]
        ref = []
        for g in groups:
            x = float(np.median(np.mod(g, L)))
            could = sum(self._seen(p, x) for p in passes)
            hits = sum(any(abs(circ(w[0] - x, L)) <= MATCH for w in p["wheels"]) for p in passes)
            if could and hits >= min(2, could) and hits >= 0.5 * could:
                ref.append(x)
        return ref

    def _evaluate(self, passes, loop):
        L = self.L
        passes = self._registered(passes)
        ref = self._reference(passes)
        if not ref:
            return
        covered = np.zeros(self.N, bool)               # godets some pass could read
        for p in passes:
            cells = np.flatnonzero(p["cover"])
            covered[((cells * COVER_RES - self.phase) % L // self.P).astype(int) % self.N] = True
        count = np.zeros(self.N, int)
        for x in ref:
            count[self.godet(x)] += 1
        latest = passes[-1]
        wheels_by_col = [w[1] for w in latest["wheels"]]
        found = []
        # rule A: >= 5 consecutive covered godets with no wheel (circular)
        empty = (count == 0) & covered
        if (~empty).any():
            start = int(np.argmax(~empty)); run = []
            for g in [(start + i) % self.N for i in range(self.N)] + [start]:
                if empty[g]:
                    run.append(g)
                else:
                    if len(run) >= GAP_GODETS:
                        found.append(("wheel_gap", run[0], run[-1], {"godets": len(run), "wheels": 0}))
                    run = []
        # rule B: >= 2 wheels within any 3 consecutive godets (windows merged)
        hot = [g for g in range(self.N) if sum(count[(g + k) % self.N] for k in range(DENSE_WINDOW)) >= DENSE_MIN]
        merged = []                                                # [first, last] unwrapped (last may pass N)
        for g in hot:                                              # hot is ascending
            if merged and g <= merged[-1][1] + 1:                  # starts inside or right after
                merged[-1][1] = g + DENSE_WINDOW - 1
            else:
                merged.append([g, g + DENSE_WINDOW - 1])
        if len(merged) > 1 and merged[-1][1] + 1 >= merged[0][0] + self.N:   # joins across the loop end
            merged[0][0] = merged.pop()[0] - self.N
        merged = [[a % self.N, b % self.N] for a, b in merged]
        for a, b in merged:
            span = [(a + k) % self.N for k in range(((b - a) % self.N) + 1)]
            found.append(("wheel_density", a, b, {"godets": len(span), "wheels": int(sum(count[g] for g in span))}))
        # rule M: a wheel of the EARLIER passes' reference, absent on the 2 latest passes that
        # could see it (the latest passes must not vote on their own reference)
        if len(passes) >= 4:
            for x in self._reference(passes[:-2]):
                late = [p for p in passes[-2:] if self._seen(p, x)]
                if len(late) == 2 and not any(any(abs(circ(w[0] - x, L)) <= MATCH for w in p["wheels"]) for p in late):
                    g = self.godet(x)
                    found.append(("wheel_missing", g, g, {"godets": 1, "wheels": 0}))
        if self.trace is not None:
            self.trace.append({"loop": int(loop), "lock": self.lock_id,
                               "passes": [[len(p["wheels"]), round(float(p["cover"].mean()), 3)] for p in passes],
                               "covered_godets": int(covered.sum()), "ref": [round(x, 1) for x in ref],
                               "latest": sorted(round(w[0], 1) for w in latest["wheels"]),
                               "found": [[k, int(a), int(b)] for k, a, b, _ in found]})
        self._update_flags(found, loop, wheels_by_col)

    def _update_flags(self, found, loop, wheels_by_col):
        with self.flags_lock:
            self._update_flags_locked(found, loop, wheels_by_col)

    def _update_flags_locked(self, found, loop, wheels_by_col):
        active = set()
        for kind, first, last, meas in found:
            # any earlier flag of this kind at this place, active or not: a place that drops out for
            # one evaluation or moves by a godet keeps its key (2026-09-07 replay: 68 keys for ~25 places)
            # (a missing wheel is a change: once it has cleared, a new loss is a new alert)
            match = next((k for k, f in self.flags.items() if f["kind"] == kind and k not in active
                          and (f["active_loop"] >= 0 or kind != "wheel_missing")
                          and self._close(f, first, last)), None)
            if match is None:
                tag = kind.upper()
                key = (f"{self.cam}:{tag}:{self.ext(first)}:{loop}" if kind == "wheel_missing"
                       else f"{self.cam}:{tag}:{self.ext(first)}")
                while key in self.flags:
                    key += "b"
                self.flags[key] = {"kind": kind, "first": first, "last": last, "loop": loop,
                                   "raised_at": time.time(), "evidence": None, "active_loop": loop, **meas}
                match = key
            f = self.flags[match]
            f.update({"first": first, "last": last, "active_loop": loop, **meas})
            active.add(match)
            if f["evidence"] is None:
                ev = self._evidence(first, last, wheels_by_col)
                if ev is not None and self.evidence_sink is not None:
                    fid = str(uuid.uuid4())
                    self.evidence_sink({"frame_id": fid, "sequence_no": 0, "captured_at": None, "s": 0.0,
                                        "jpeg": ev[0]})
                    f["evidence"] = {"frame_id": fid, "box": ev[1]}
        for k, f in self.flags.items():
            if k not in active and f["active_loop"] >= 0:
                f["active_loop"] = -1                  # no longer seen: kept, no longer reported

    def _close(self, f, first, last):
        span_a = {(f["first"] + k) % self.N for k in range(((f["last"] - f["first"]) % self.N) + 1)}
        span_b = {(first + k) % self.N for k in range(((last - first) % self.N) + 1)}
        return any(abs(circ(a - b, self.N)) <= FLAG_TOL for a in span_a for b in span_b)

    # ---- Tier 2 ------------------------------------------------------------------------------
    def events(self, pb2_response, requested=()):
        with self.flags_lock:
            flags = {k: dict(f) for k, f in self.flags.items()}
        for key, f in sorted(flags.items()):
            if f["active_loop"] < 0:
                continue
            gid = self.ext(f["first"])
            if requested and gid not in requested:
                continue
            ev = pb2_response.events.add()
            ev.event_key = key
            ev.kind = f["kind"]
            ev.godet_id = gid
            ev.loop_no = int(f["loop"])
            ev.state = "confirmed"
            ev.measurements["first_godet"] = float(self.ext(f["first"]))
            ev.measurements["last_godet"] = float(self.ext(f["last"]))
            ev.measurements["godets"] = float(f["godets"])
            ev.measurements["wheels"] = float(f["wheels"])
            if f["evidence"]:
                ev.evidence_frame_id = f["evidence"]["frame_id"]
                bx = f["evidence"]["box"]
                ev.evidence_box.x, ev.evidence_box.y = bx[0], bx[1]
                ev.evidence_box.width, ev.evidence_box.height = bx[2], bx[3]

    def active_counts(self):
        out = {"wheel_gap": 0, "wheel_density": 0, "wheel_missing": 0}
        with self.flags_lock:
            flags = list(self.flags.values())
        for f in flags:
            if f["active_loop"] >= 0:
                out[f["kind"]] += 1
        return out

    # ---- persistence -------------------------------------------------------------------------
    def snapshot(self):
        """Flags only: passes rebuild within 2 loops after a restart (a restart re-locks anyway)."""
        with self.flags_lock:
            return {"flags": {k: dict(f) for k, f in self.flags.items()}, "counters": dict(self.counters)}

    def restore(self, snap):
        self.flags = {k: dict(f) for k, f in (snap or {}).get("flags", {}).items()}
        self.counters.update((snap or {}).get("counters", {}))
