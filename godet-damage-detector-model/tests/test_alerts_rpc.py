"""Tier-2 RPC: GetGodetState serves persistent/pending/confirmed + health.

Feeds h00 slots (fast, no video), then calls the servicer directly. Known
batch truth: alerts.csv has pending godets 1172/224/236; health is all clear.

Run:  python -m pytest tests/test_alerts_rpc.py -q -s
"""
import sys
from pathlib import Path

from support import CAM, ROOT, slots_from_captures

sys.path.insert(0, str(ROOT / "src" / "gen"))
sys.path.insert(0, str(ROOT))

import inference_v2_pb2 as pb2  # noqa: E402
from src.alerts import AlertTracker  # noqa: E402
from src.bundle import load_bundle  # noqa: E402
from src.identity import StreamingIdentity  # noqa: E402
from src.server import RealServicer  # noqa: E402


def build_servicer(tmp_path):
    bundle = load_bundle(ROOT / "model")
    import grpc_health.v1.health as health_mod

    health = health_mod.HealthServicer()
    svc = RealServicer.__new__(RealServicer)
    # bypass __init__ (no store/network): wire the stack manually
    from src.pipeline import StreamProcessor

    svc.bundle = bundle
    svc.pipe = StreamProcessor(bundle)
    svc.ident = StreamingIdentity(bundle.master, bundle.loop_slots, bundle.rules)
    svc.alerts = AlertTracker(bundle.rules, bundle.lines)
    import threading

    svc._lock = threading.Lock()
    svc.counters = {"frames_total": 0, "dead_letters_total": 0,
                    "sequence_gaps_total": 0, "late_frames_total": 0}
    svc.max_jpeg_bytes = 8_000_000
    svc.pipe.subscribe(svc.ident.on_slot)
    svc.ident.subscribe(svc.alerts.on_godet_row)
    svc.ident.subscribe_loops(svc.alerts.on_loop_start)
    return svc


def test_tier2_rpc(tmp_path):
    svc = build_servicer(tmp_path)
    for rec in slots_from_captures(CAM / "out" / "h00" / "captures.csv"):
        svc.ident.on_slot(rec)
    svc.ident.flush()
    svc.alerts.flush()

    r = svc.GetGodetState(pb2.GodetStateRequest(), None)
    assert r.model_version == svc.bundle.version
    by_id = {g.godet_id: g for g in r.godets}
    for gid in (1172, 224, 236):
        assert gid in by_id, f"godet {gid} missing from Tier-2"
        assert by_id[gid].state == "pending", f"godet {gid} state {by_id[gid].state}"
    event_keys = {e.event_key for e in r.events}
    assert {"DAMAGE:1172:3", "DAMAGE:224:3", "DAMAGE:236:3"} <= event_keys
    assert all(e.evidence_frame_id for e in r.events)
    assert r.health.ready and r.health.loop_locked
    assert r.health.status == "ok", r.health.detail
    print(f"\nTier-2 serves {len(r.godets)} godets, health={r.health.status}")

    r2 = svc.GetGodetState(pb2.GodetStateRequest(godet_ids=[224], include_history=True), None)
    assert len(r2.godets) == 1 and r2.godets[0].godet_id == 224
    assert len(r2.godets[0].lip_history) > 0
