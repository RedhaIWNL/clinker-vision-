"""gRPC server: Infer bidi-stream + GetGodetState + standard health.

Run from the repo root::

    python -m src.server --bind 0.0.0.0:50051 --mock              # canned mock
    python -m src.server --bind 0.0.0.0:50051 --bundle-dir model  # real detector

Sequencing (real mode): frames are released to the detector strictly in
sequence_no order. A jump holds later frames (up to REORDER_WINDOW=64); a gap
still missing after 64 subsequent arrivals is declared dropped and converted to
virtual slots (nominal 25 fps / 0.8 s period). Late frames (seq < expected) are
counted and skipped. Per-frame input errors dead-letter the frame (log +
counter, NO response, stream stays up); only server-side failures abort streams.
"""
from __future__ import annotations

import argparse
import logging
import os
import signal
import sys
import threading
import time
from concurrent import futures
from pathlib import Path

import grpc

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src" / "gen"))  # protoc stubs import each other top-level
sys.path.insert(0, str(ROOT))

from src import mock as mock_mod  # noqa: E402
import inference_v2_pb2 as pb2  # noqa: E402
import inference_v2_pb2_grpc as pb2_grpc  # noqa: E402

LOG = logging.getLogger("clinker-vision-model")

SUPPORTED_CAMERAS = ("CAM-1",)
DEFAULT_MAX_JPEG_BYTES = 8_000_000
REORDER_WINDOW = 64       # §0 freeze: hold up to 64 frames past a gap
NOMINAL_FPS = 25.0        # camera frame rate (godet period varies; fps does not)
PERIOD_S = 0.8
GAP_MISS_S = 1.2


def jpeg_decodable(image_data: bytes) -> bool:
    """True iff OpenCV can decode the bytes to a non-empty greyscale image."""
    import cv2
    import numpy as np

    if not image_data:
        return False
    img = cv2.imdecode(np.frombuffer(image_data, dtype=np.uint8), cv2.IMREAD_GRAYSCALE)
    return img is not None and img.size > 0


def tier1_from_dict(pb2_mod, frame_id: str, model_version: str, d: dict):
    """Map a pipeline Tier-1 dict to InferenceResponse. Detection (fixed ROI box)
    appears ONLY on captures whose lip measured short (unconfirmed indicator —
    Tier-2 confirms; documented in contract §3)."""
    from src.pipeline import FIXED_BOX

    r = pb2_mod.InferenceResponse()
    r.frame_id = frame_id
    if d.get("is_capture") and d.get("indicator"):
        det = r.detections.add()
        det.observation_target = "GODET"
        det.fault_type = "DAMAGE"
        det.bounding_box.x = FIXED_BOX["x"]
        det.bounding_box.y = FIXED_BOX["y"]
        det.bounding_box.width = FIXED_BOX["width"]
        det.bounding_box.height = FIXED_BOX["height"]
    for k in ("lit_L", "lit_R", "peak", "dx", "dy", "slot", "status_code"):
        if k in d and d[k] == d[k]:  # skip NaN
            r.scalar_measurements[k] = float(d[k])
    r.model_version = model_version
    r.processed_at.FromDatetime(__import__("datetime").datetime.now(
        __import__("datetime").timezone.utc))
    return r


class MockServicer(pb2_grpc.InferenceServiceServicer):
    """Canned responder used for deterministic integration tests."""

    def __init__(self, max_jpeg_bytes: int):
        self.max_jpeg_bytes = max_jpeg_bytes
        self.frames_total = 0
        self.dead_letters_total = 0
        self._lock = threading.Lock()

    def _reject(self, frame_id: str, camera_id: str, reason: str):
        with self._lock:
            self.dead_letters_total += 1
        LOG.warning("dead-letter frame_id=%s camera_id=%s reason=%s", frame_id, camera_id, reason)

    def Infer(self, request_iterator, context):
        for req in request_iterator:
            if not context.is_active():
                break
            if req.camera_id not in SUPPORTED_CAMERAS:
                self._reject(req.frame_id, req.camera_id, "unknown camera_id")
                continue
            if len(req.image_data) > self.max_jpeg_bytes:
                self._reject(req.frame_id, req.camera_id, "oversized jpeg")
                continue
            if not jpeg_decodable(req.image_data):
                self._reject(req.frame_id, req.camera_id, "undecodable jpeg")
                continue
            with self._lock:
                self.frames_total += 1
            yield mock_mod.build_mock_response(pb2, req.frame_id, req.image_data)

    def GetGodetState(self, request, context):
        return mock_mod.build_mock_state(pb2)


class RealServicer(pb2_grpc.InferenceServiceServicer):
    """Phase 2 real Tier-1 (measurement) + Phase 3 Tier-2 (identity/alerts).

    Wiring: frames -> StreamProcessor -> slot records -> StreamingIdentity
    -> godet rows -> AlertTracker -> GetGodetState. Snapshots persist every
    completed loop; restarts restore alignment + rows + alert keys when the
    bundle version matches (else a fresh history begins)."""

    def __init__(self, bundle_dir: str, max_jpeg_bytes: int, store_path: str,
                 health_servicer, frame_deadline_s: float = 5.0):
        from src.alerts import AlertTracker
        from src.bundle import load_bundle
        from src.identity import StreamingIdentity
        from src.pipeline import StreamProcessor
        from src.store import Store

        t0 = time.time()
        self.bundle = load_bundle(bundle_dir)  # raises BundleError: fail startup
        self.pipe = StreamProcessor(self.bundle)
        self.ident = StreamingIdentity(self.bundle.master, self.bundle.loop_slots,
                                       self.bundle.rules)
        self.alerts = AlertTracker(self.bundle.rules, self.bundle.lines)
        self.store = Store(store_path)
        self.health_servicer = health_servicer
        self.serving = False
        self.max_jpeg_bytes = max_jpeg_bytes
        self.expected = None       # next sequence_no to release
        self.hold = {}             # seq -> req (reorder window)
        self.counters = {"frames_total": 0, "dead_letters_total": 0,
                         "sequence_gaps_total": 0, "late_frames_total": 0,
                         "deadline_exceeded_total": 0}
        self._lock = threading.Lock()
        self._tier2_key = None  # last logged (locked, resyncing, chain) triple
        self.frame_deadline_s = frame_deadline_s
        self._snap_slot = 0      # pipe.slot at last snapshot (periodic, § below)
        self._saved_rows = 0     # godet rows already persisted (incremental)
        self.SNAP_EVERY = 200    # slots between periodic snapshots: crash loss
                                 # is bounded to the trailing partial window
        self.pipe.subscribe(self.ident.on_slot)
        self.ident.subscribe(self.alerts.on_godet_row)
        self.ident.subscribe_loops(self.alerts.on_loop_start)
        self.alerts.on_complete(self._snapshot)
        restored = self.store.load(self.bundle.version)
        if restored is not None:
            self.ident.restore(restored["identity"])
            self.ident.restore_tail(restored["tail"])
            self.alerts.restore({"rows": restored["rows"],
                                 "loop_starts": restored["loop_starts"],
                                 "computed": restored["computed"],
                                 "seen_alert_keys": restored["alert_keys"]})
            # loop_starts for completion detection come from identity events on
            # new loops; re-seed from restored identity starts:
            for k, st in enumerate(restored["identity"].get("starts", [])):
                self.alerts.on_loop_start(k, st)
            LOG.info("store restored version=%s loops=%s rows=%d",
                     self.bundle.version, sorted(restored["loops"]),
                     len(restored["rows"]))
        LOG.info("bundle loaded dir=%s version=%s in %.1fs",
                 bundle_dir, self.bundle.version, time.time() - t0)

    def _snapshot(self, loop_done):
        self._persist()
        LOG.info("snapshot saved loop=%d rows=%d", loop_done, self._saved_rows)

    def _persist(self):
        """Persist incrementally: only godet rows not yet saved (upsert by
        loop+slot makes every save idempotent)."""
        try:
            asnap = self.alerts.snapshot()
            new_rows = asnap["rows"][self._saved_rows:]
            self.store.save(self.bundle.version, self.ident.snapshot(),
                            new_rows, asnap["computed"],
                            asnap["seen_alert_keys"], asnap["loop_starts"])
            self._saved_rows = len(asnap["rows"])
        except Exception as e:
            LOG.error("snapshot failed: %s", e)

    def _snapshot_periodic(self):
        if self.pipe.slot - self._snap_slot >= self.SNAP_EVERY:
            self._snap_slot = self.pipe.slot
            self._persist()

    def _maybe_serve(self):
        from grpc_health.v1 import health_pb2

        if self.ident.locked and not self.serving:
            self.serving = True
            self.health_servicer.set(
                "", health_pb2.HealthCheckResponse.SERVING)
            LOG.info("loop locked: health SERVING")

    # -- sequencing ------------------------------------------------------
    def _declare_gap(self, missing_from: int, missing_to: int):
        """Sequence range [missing_from, missing_to) never arrived: estimate the
        godets it hid (nominal fps / godet period) as virtual slots."""
        n_missing = missing_to - missing_from
        gap_s = n_missing / NOMINAL_FPS
        n_virtual = int(round(gap_s / PERIOD_S)) - 1 if gap_s > GAP_MISS_S else 0
        for _ in range(max(n_virtual, 0)):
            self.pipe.virtual_slot()
        with self._lock:
            self.counters["sequence_gaps_total"] += 1
        LOG.warning("sequence gap seq=%d..%d (%d frames, %.2fs) -> %d virtual slots",
                    missing_from, missing_to - 1, n_missing, gap_s, max(n_virtual, 0))

    def _log_tier2_transitions(self):
        """Log Tier-2 state changes exactly once (lock, resync, chain alarm)."""
        key = (self.ident.locked, self.ident.resyncing, self.ident.chain_changed)
        if key != self._tier2_key:
            self._tier2_key = key
            LOG.info("tier2 locked=%s resyncing=%s chain_changed=%s",
                     *key)

    def _release(self, req, out):
        """Process one in-order request, appending the Tier-1 response (if any).

        Cancellation granularity is per frame (context is checked by the Infer
        loop between frames; a single frame costs tens of ms). A frame whose
        processing exceeds frame_deadline_s is skipped like a dead letter and
        counted (the pipeline treats it as a latency metric, not a retry: the
        frame's sequence position is still consumed)."""
        if req.camera_id not in SUPPORTED_CAMERAS:
            self._bump("dead_letters_total")
            LOG.warning("dead-letter frame_id=%s camera_id=%s reason=unknown camera_id",
                        req.frame_id, req.camera_id)
            return
        if len(req.image_data) > self.max_jpeg_bytes:
            self._bump("dead_letters_total")
            LOG.warning("dead-letter frame_id=%s reason=oversized jpeg", req.frame_id)
            return
        t0 = time.time()
        d, ok = self.pipe.on_frame(req.image_data, req.frame_id, req.sequence_no)
        dt = time.time() - t0
        if not ok:
            self._bump("dead_letters_total")
            LOG.warning("dead-letter frame_id=%s reason=undecodable-or-miscropped jpeg",
                        req.frame_id)
            return
        if dt > self.frame_deadline_s:
            self._bump("deadline_exceeded_total")
            LOG.warning("deadline frame_id=%s took %.2fs > %.2fs: counted, no response",
                        req.frame_id, dt, self.frame_deadline_s)
            return
        self._bump("frames_total")
        if d is not None:
            out.append(tier1_from_dict(pb2, req.frame_id, self.bundle.version, d))
        self._maybe_serve()
        self._log_tier2_transitions()

    def _bump(self, key: str):
        with self._lock:
            self.counters[key] += 1

    def Infer(self, request_iterator, context):
        out = []
        for req in request_iterator:
            if not context.is_active():
                break
            seq = req.sequence_no
            if self.expected is None:
                self.expected = seq
            if seq < self.expected:
                self._bump("late_frames_total")
                continue
            if seq == self.expected:
                self._release(req, out)
                self.expected += 1
                while self.expected in self.hold:  # drain in order
                    self._release(self.hold.pop(self.expected), out)
                    self.expected += 1
            else:  # seq > expected: hold for the reorder window
                self.hold[seq] = req
                if len(self.hold) > REORDER_WINDOW:
                    missing_to = min(self.hold)
                    self._declare_gap(self.expected, missing_to)
                    self.expected = missing_to
                    while self.expected in self.hold:
                        self._release(self.hold.pop(self.expected), out)
                        self.expected += 1
            for r in out:  # yield incrementally, keep latency low
                yield r
            out.clear()
            self._snapshot_periodic()

    def GetGodetState(self, request, context):
        import datetime
        import math
        import numbers
        import pandas as pd

        with self._lock:
            A = self.alerts.alerts
            S = self.alerts.splay
            P = self.alerts.persistent
            rows = list(self.alerts.rows)
        r = pb2.GodetStateResponse()
        r.model_version = self.bundle.version
        conf = set(A[A["status"] == "confirmed"]["godet_id"]) | set(
            S[S["status"] == "confirmed"]["godet_id"]) if len(A) or len(S) else set()
        pend = set(A[A["status"] == "pending"]["godet_id"]) | set(
            S[S["status"] == "pending"]["godet_id"]) if len(A) or len(S) else set()
        pers = {}
        if len(P):
            pers = dict(zip(P["godet_id"].astype(int),
                            zip(P["lip_median"], P["near_plate"].astype(bool))))
        by_gid = {}
        for row in rows:
            by_gid.setdefault(int(row["godet_id"]), []).append(row)
        requested = {int(g) for g in request.godet_ids}
        if len(A):
            for _, alert in A[A["status"].isin(("pending", "confirmed"))].iterrows():
                gid = int(alert["godet_id"])
                if requested and gid not in requested:
                    continue
                loop_no = int(alert["loop"])
                event = r.events.add()
                event.event_key = f"DAMAGE:{gid}:{loop_no}"
                event.kind = "damage"
                event.godet_id = gid
                event.loop_no = loop_no
                event.state = str(alert["status"])
                candidates = [x for x in by_gid.get(gid, [])
                              if int(x.get("loop", -1)) == loop_no
                              and x.get("frame_id")]
                if candidates:
                    candidate = max(candidates, key=lambda x: int(x.get("slot", -1)))
                    event.evidence_frame_id = str(candidate["frame_id"])
                    t_left = candidate.get("t_left")
                    if isinstance(t_left, numbers.Real) and math.isfinite(float(t_left)):
                        event.occurred_at.FromDatetime(
                            datetime.datetime.fromtimestamp(float(t_left), datetime.timezone.utc))
                payload = dict(alert)
                for key in ("lip_before", "lip_now", "drop"):
                    value = payload.get(key)
                    if isinstance(value, numbers.Real) and math.isfinite(float(value)):
                        event.measurements[key] = float(value)
        wanted = [int(g) for g in request.godet_ids] or sorted(
            set(conf) | set(pend) | set(pers))
        for g in wanted:
            gs = by_gid.get(g, [])
            st = ("confirmed" if g in conf else
                  "pending" if g in pend else
                  "persistent" if g in pers else "healthy")
            gr = r.godets.add()
            gr.godet_id = g
            gr.state = st
            gr.near_plate = bool(pers.get(g, (0.0, False))[1])
            if gs:
                last = max(gs, key=lambda x: (x["loop"], x["slot"]))
                gr.lip = float(last["lip"]) if last["lip"] == last["lip"] else 0.0
                gr.last_seen_loop = int(last["loop"])
                if request.include_history:
                    loops = sorted({x["loop"] for x in gs})[-3:]
                    for lo in loops:
                        vals = [x["lip"] for x in gs if x["loop"] == lo
                                and x["lip"] == x["lip"]]
                        gr.lip_history.append(float(sum(vals) / len(vals)) if vals else 0.0)
        h = r.health
        h.ready = self.ident.locked
        h.loop_locked = self.ident.locked
        lines = list(self.alerts.health_lines)
        import statistics

        fp = list(self.pipe.frame_peaks)
        if not self.ident.locked and len(fp) >= 100 and statistics.median(fp) < 0.5:
            # live startup self-test: nothing godet-like in view for 100+ frames
            LOG.error("startup self-test FAILED: median frame peak %.2f < 0.5 "
                      "over %d frames (camera moved / wrong feed?)", statistics.median(fp), len(fp))
            lines.append("TEMPLATE LOST (camera moved / ROI changed?): median frame peak < 0.5")
        drift = self.ident.loop_drift_est
        if drift is not None and abs(drift - self.bundle.loop_slots) / self.bundle.loop_slots > 0.03:
            lines.append(f"LOOP DRIFT: re-estimated {drift} slots vs pinned "
                         f"{self.bundle.loop_slots} -> chain changed, rebuild master")
        if self.ident.chain_changed:
            status = "resyncing"
            detail = "chain order changed: rebuild master (runbook)" + (
                "; " + "; ".join(lines) if lines else "")
        elif self.ident.resyncing:
            status, detail = "resyncing", "loop re-alignment in progress"
        elif any(l.startswith("TEMPLATE LOST") for l in lines):
            status, detail = "template_lost", "; ".join(lines)
        elif not self.ident.locked:
            status, detail = "not_ready", "collecting first loop+300 slots for alignment"
        elif any(l.startswith("TRIGGER LOST") for l in lines):
            status, detail = "trigger_lost", "; ".join(lines)
        elif any(l.startswith("POPULATION") for l in lines):
            status, detail = "population_alarm", "; ".join(lines)
        else:
            status, detail = "ok", "; ".join(lines) if lines else "all clear"
        h.status, h.detail = status, detail
        with self._lock:
            items = dict(self.counters)
        items["captures_total"] = self.pipe.counters["captures_total"]
        items["virtual_slots_total"] = self.pipe.counters["virtual_slots_total"]
        items["godet_rows"] = len(rows)
        for k, v in items.items():
            h.counters[k] = float(v)
        return r


def create_server(bind: str, max_jpeg_bytes: int, bundle_dir: str | None,
                  mock: bool, store_path: str = "state/store.db",
                  frame_deadline_s: float = 5.0) -> grpc.Server:
    from grpc_health.v1 import health as health_mod
    from grpc_health.v1 import health_pb2, health_pb2_grpc

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=8))
    health_servicer = health_mod.HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)
    if mock:
        servicer = MockServicer(max_jpeg_bytes)
        # Mock has no loop to lock: SERVING means "accepting streams".
        health_servicer.set("", health_pb2.HealthCheckResponse.SERVING)
    else:
        servicer = RealServicer(bundle_dir, max_jpeg_bytes, store_path,
                                health_servicer, frame_deadline_s)
        # Real mode starts NOT_SERVING; flips on first loop lock (Phase 3 rule:
        # SERVING ⇔ loop locked). Until then Tier-1 flows, Tier-2 says not_ready.
        health_servicer.set("", health_pb2.HealthCheckResponse.NOT_SERVING)
    pb2_grpc.add_InferenceServiceServicer_to_server(servicer, server)
    server.add_insecure_port(bind)
    server.servicer = servicer  # for tests/introspection
    return server


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--bind", default="0.0.0.0:50051")
    ap.add_argument("--mock", action="store_true", help="canned responses (no CV)")
    ap.add_argument("--bundle-dir", default=None, help="model/ bundle for real mode")
    ap.add_argument("--store-path", default="state/store.db",
                    help="SQLite snapshot path (restart recovery)")
    ap.add_argument("--max-jpeg-bytes", type=int, default=DEFAULT_MAX_JPEG_BYTES)
    ap.add_argument("--frame-deadline-s", type=float, default=5.0,
                    help="per-frame processing deadline; slower frames are counted "
                         "and skipped (no response), never retried")
    ap.add_argument("--shutdown-grace-s", type=float, default=5.0)
    ap.add_argument("--log-level", default="INFO")
    ap.add_argument("--model-build", default=os.environ.get("MODEL_BUILD", "dev"),
                    help="build tag baked into model_version")
    a = ap.parse_args()

    logging.basicConfig(level=getattr(logging, a.log_level.upper(), logging.INFO),
                        format="%(asctime)s %(levelname)s %(name)s %(message)s")
    os.environ["MODEL_BUILD"] = a.model_build

    if a.bundle_dir is None and not a.mock:
        LOG.error("nothing to serve: pass --bundle-dir (real) or --mock")
        return 2

    try:
        server = create_server(a.bind, a.max_jpeg_bytes, a.bundle_dir, a.mock,
                               a.store_path, a.frame_deadline_s)
    except Exception as e:  # BundleError etc: fail startup loudly, non-zero
        LOG.error("startup failed: %s", e)
        return 1
    mode = "mock" if a.mock else "real"
    ver = mock_mod.MODEL_VERSION if a.mock else server.servicer.bundle.version
    server.start()
    LOG.info("startup bind=%s mode=%s model_version=%s max_jpeg_bytes=%d",
             a.bind, mode, ver, a.max_jpeg_bytes)

    stop = threading.Event()

    def _handle(signum, frame):
        LOG.info("shutdown signal=%s: persisting state, draining in-flight", signum)
        servicer = server.servicer
        if hasattr(servicer, "_persist"):
            servicer._persist()  # final snapshot: at most the in-flight tail is lost
            LOG.info("shutdown snapshot saved")
        stop.set()

    signal.signal(signal.SIGTERM, _handle)
    signal.signal(signal.SIGINT, _handle)
    stop.wait()
    server.stop(grace=a.shutdown_grace_s)
    LOG.info("shutdown complete")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
