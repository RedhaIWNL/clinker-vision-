"""Scenario tests (plan matrix #6,8,10-13): conveyor stop, resync correction,
stream reconnect, pending/confirmed events + dedup.

Run:  python -m pytest tests/test_scenarios.py -q -s
"""
import copy
import sys
from pathlib import Path

import pandas as pd

from support import CAM, ROOT, black_full_jpeg

sys.path.insert(0, str(ROOT / "src" / "gen"))
sys.path.insert(0, str(ROOT))

import inference_v2_pb2 as pb2  # noqa: E402
from src.alerts import AlertTracker  # noqa: E402
from src.bundle import load_bundle  # noqa: E402
from src.identity import StreamingIdentity  # noqa: E402
from src.pipeline import StreamProcessor  # noqa: E402
from support import slots_from_captures  # noqa: E402

H00 = CAM / "out" / "h00"


def make_stack(bundle):
    ident = StreamingIdentity(bundle.master, bundle.loop_slots, bundle.rules)
    alerts = AlertTracker(bundle.rules, bundle.lines)
    ident.subscribe(alerts.on_godet_row)
    ident.subscribe_loops(alerts.on_loop_start)
    return ident, alerts


def test_conveyor_stop_no_captures():
    import numpy as np

    bundle = load_bundle(ROOT / "model")
    p = StreamProcessor(bundle)
    roi = np.full((258, 144), 128, dtype=np.uint8)  # frozen frame, repeated
    n_cap = 0
    for i in range(60):
        if p.on_roi(roi.copy(), frame_id=f"s-{i}", sequence_no=i)["is_capture"]:
            n_cap += 1
    assert n_cap == 0, "frozen frames must not trigger captures"
    print("\nconveyor stop: 60 frozen frames -> 0 captures")


def test_trigger_lost_rule():
    from collections import deque

    bundle = load_bundle(ROOT / "model")
    alerts = AlertTracker(bundle.rules, bundle.lines)
    alerts.cap_times = deque(list(range(0, 120)) + [1000] * 10)
    lines = alerts._health(pd.DataFrame())
    assert any(l.startswith("TRIGGER LOST") for l in lines), lines
    print("\ntrigger rule fires on a stopped-then-trickling stream")


def test_resync_correction():
    bundle = load_bundle(ROOT / "model")
    ident, alerts = make_stack(bundle)
    recs = slots_from_captures(H00 / "captures.csv")
    for rec in recs:
        ident.on_slot(rec)
    assert ident.starts == [0, 1208, 2416, 3624]
    # extra loop rotated +5 pattern positions: provisional 4832 must correct
    seg = recs[1208:2416]
    fed = [copy.copy(r) for r in seg[1208 - 341:] + seg[:1208 - 341]]
    for r in fed:  # fresh objects, renumbered (as production would send)
        r.row_done = False
        for k in ("gid_assigned", "loop_assigned"):
            if hasattr(r, k):
                delattr(r, k)
    for i, r in enumerate(fed):
        r.slot = 4496 + i
        ident.on_slot(r)
    print(f"\nshifted loop: starts[-1]={ident.starts[-1]} resyncing={ident.resyncing}")
    assert ident.starts[-1] == 4837, f"verify did not correct: {ident.starts[-1]}"
    assert not ident.resyncing


def test_stream_reconnect_no_duplicates(tmp_path):
    import grpc_health.v1.health as health_mod

    from src.server import RealServicer

    svc = RealServicer(str(ROOT / "model"), 8_000_000, str(tmp_path / "s.db"),
                       health_mod.HealthServicer())
    jpeg = black_full_jpeg()
    out = []

    def send(seq):
        req = pb2.InferenceRequest(frame_id=f"r-{seq}", camera_id="CAM-1",
                                   image_data=jpeg, sequence_no=seq)
        if svc.expected is None:
            svc.expected = seq
        if req.sequence_no < svc.expected:
            from src.server import RealServicer as _R  # noqa

            svc._bump("late_frames_total")
            return
        assert req.sequence_no == svc.expected
        svc._release(req, out)
        svc.expected += 1

    for s in range(100):
        send(s)
    for s in range(90, 200):  # reconnect replays 90..99, then continues
        send(s)
    ids = [r.frame_id for r in out]
    assert len(ids) == len(set(ids)) == 200, "duplicate or missing Tier-1"
    assert svc.counters["late_frames_total"] == 10
    print("\nreconnect: 10-frame overlap -> 10 late, 200 unique Tier-1")


def test_pending_events_and_idempotent_emit():
    bundle = load_bundle(ROOT / "model")
    ident, alerts = make_stack(bundle)
    for rec in slots_from_captures(H00 / "captures.csv"):
        ident.on_slot(rec)
    ident.flush()
    alerts.flush()
    pend = {(e[0], e[1]) for e in alerts.events if e[3] == "pending"}
    assert {("damage", 1172), ("damage", 224), ("damage", 236)} <= pend
    assert not [e for e in alerts.events if e[3] == "confirmed"], "h00 has no confirmed"
    n = len(alerts.events)
    alerts.flush()  # second flush must not duplicate
    assert len(alerts.events) == n
    print(f"\nevents: {len(alerts.events)} total, pending {sorted(pend)}, re-flush stable")
