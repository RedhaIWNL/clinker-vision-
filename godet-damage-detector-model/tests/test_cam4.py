"""CAM-4 (day side plates): bundle, engine rules, evidence frames and server wiring.

Fast tests use synthetic frames and passes. The real-video test decodes 600 frames of the
2026-09-07 10:00 hour (skipped when the calibration repo's video is not available); the
full-hour quality gate is tools/cam4_replay.py (plan step 3), not a unit test.

Run:  python -m pytest tests/test_cam4.py -q
"""
import os
import shutil
import sys
from pathlib import Path

import grpc
import numpy as np
import pytest

ROOT = Path(__file__).resolve().parents[1]
CAM = Path(os.environ.get("CLINKER_CALIB_ROOT", ROOT.parent))
sys.path.insert(0, str(ROOT / "src" / "gen"))
sys.path.insert(0, str(ROOT))

import inference_v2_pb2 as pb2  # noqa: E402
import inference_v2_pb2_grpc as pb2_grpc  # noqa: E402
from src.bundle import BundleError  # noqa: E402
from src.cam4.bundle import load_cam4_bundle  # noqa: E402
from src.cam4.engine import Cam4Engine  # noqa: E402
from src.cam4.identity import PassRecord  # noqa: E402
from src.server import create_server  # noqa: E402
from src.store import Store  # noqa: E402

BUNDLE = ROOT / "model" / "cam4"
VIDEO = CAM / "data" / "camera_4" / "day" / "NVR_ch4_main_20260907100000_20260907110000.mp4"


def jpeg(img, quality=90):
    import cv2

    ok, buf = cv2.imencode(".jpg", img, [cv2.IMWRITE_JPEG_QUALITY, quality])
    assert ok
    return bytes(buf)


def grey_frame(w=2688, h=1520, value=90):
    return jpeg(np.full((h, w), value, np.uint8))


def video_frames(start, count):
    import cv2

    cap = cv2.VideoCapture(str(VIDEO))
    assert cap.isOpened()
    cap.set(cv2.CAP_PROP_POS_FRAMES, start)
    out = []
    for _ in range(count):
        ok, f = cap.read()
        assert ok
        out.append(jpeg(f))
    cap.release()
    return out


class Req:
    def __init__(self, seq):
        self.sequence_no = seq


# ---- bundle -------------------------------------------------------------------------------
def test_bundle_loads_and_names_version():
    b = load_cam4_bundle(BUNDLE)
    assert b.ident["godets"] == 1210
    assert b.chainmap.shape == (2, b.ident["loop_cols"])
    assert b.version.endswith("+c4d-20260927")


def test_tampered_bundle_is_refused(tmp_path):
    d = tmp_path / "cam4"
    shutil.copytree(BUNDLE, d)
    cal = d / "calibration.json"
    cal.write_bytes(cal.read_bytes().replace(b'"godets": 1210', b'"godets": 1209'))
    with pytest.raises(BundleError, match="checksum"):
        load_cam4_bundle(d)


# ---- sequencing lane ---------------------------------------------------------------------
def test_sequence_restart_is_accepted_not_dropped():
    eng = Cam4Engine(BUNDLE)
    released = []
    for s in range(1, 301):
        eng.sequence(Req(s), lambda r: released.append(r.sequence_no))
    for s in range(1, 51):                      # the sender restarted (new window)
        eng.sequence(Req(s), lambda r: released.append(r.sequence_no))
    assert released == list(range(1, 301)) + list(range(1, 51))
    assert eng.counters["sequence_restarts_total"] == 1
    assert eng.counters["late_frames_total"] == 0


def test_reorder_window_still_orders_frames():
    eng = Cam4Engine(BUNDLE)
    released = []
    for s in [1, 2, 4, 3, 5]:
        eng.sequence(Req(s), lambda r: released.append(r.sequence_no))
    assert released == [1, 2, 3, 4, 5]


def test_wrong_frame_size_is_rejected():
    eng = Cam4Engine(BUNDLE)
    d, ok = eng.on_frame(grey_frame(1920, 1080), "f-1", 1)
    assert not ok and d is None
    d, ok = eng.on_frame(b"not a jpeg", "f-2", 2)
    assert not ok


# ---- history, ids, events, evidence -----------------------------------------------------
def confirmed_engine(store=None):
    """Engine whose history holds a healthy population and godet 0 confirmed on loop 2,
    with a retained evidence frame near its strongest column."""
    eng = Cam4Engine(BUNDLE, store)
    pad = eng.b.pad
    rng = np.random.default_rng(0)
    col = 5000
    for loop in range(3):
        for g in range(1, 1210):                # healthy chain: raw ~ N(1, 0.3)
            eng._on_pass(PassRecord(godet_id=g, loop=loop, raw=float(rng.normal(1, 0.3)),
                                    best_col=col, top_row=100.0, columns=161))
        if loop >= 1:                           # godet 0 (reported 1210): strong on loops 1, 2
            fid = f"ev-{loop}"
            eng.retained[fid] = {"frame_id": fid, "sequence_no": 1000 + loop,
                                 "captured_at": None, "s": float(col + 40 - pad)}
            eng._on_pass(PassRecord(godet_id=0, loop=loop, raw=9.0, best_col=col,
                                    top_row=100.0, columns=161))
        col += 1000
    return eng


def test_godet_zero_is_reported_as_1210_with_camera_event_key():
    eng = confirmed_engine()
    st = eng.godet_state(pb2.GodetStateRequest(camera_id="CAM-4", include_history=True), pb2)
    assert st.camera_id == "CAM-4"
    assert all(1 <= g.godet_id <= 1210 for g in st.godets)
    ev = {e.godet_id: e for e in st.events}
    assert 1210 in ev
    e = ev[1210]
    assert e.event_key == "CAM-4:DAMAGE:1210:2"   # confirmed on its second strong pass
    assert e.state == "confirmed" and e.kind == "damage"
    assert e.evidence_frame_id == "ev-2"          # latest strong pass
    b = e.evidence_box
    assert 0 <= b.x <= 1 and 0 <= b.y <= 1 and 0 < b.width <= 1 and 0 < b.height <= 1
    assert e.measurements["severity"] >= eng.b.rules["godet_alarm_relative"]
    g = {x.godet_id: x for x in st.godets}[1210]
    assert g.state == "confirmed" and g.passes_seen == 2 and len(g.severity_history) == 2


def test_evidence_box_follows_the_retained_frame_position():
    eng = confirmed_engine()
    e = [x for x in eng.godet_state(pb2.GodetStateRequest(), pb2).events if x.godet_id == 1210][0]
    # retained frame's slit was 40 px further along than the spot: box centre = p0 - 40 u (+ v)
    W, H = eng.b.frame_size
    cx = (e.evidence_box.x + e.evidence_box.width / 2) * W
    bb = 100.0 + 40 + 25 - eng.b.slit_half
    want = eng.b.p0 + 40 * eng.b.u + bb * eng.b.v
    assert abs(cx - want[0]) < 2


def test_history_survives_restart(tmp_path):
    store = Store(tmp_path / "store.db")
    eng = confirmed_engine(store)
    eng.persist()
    again = Cam4Engine(BUNDLE, store)
    st = again.godet_state(pb2.GodetStateRequest(), pb2)
    assert "CAM-4:DAMAGE:1210:2" in {e.event_key for e in st.events}
    assert len(again.hist.g) == 1210


def test_retained_frames_leave_in_small_batches():
    eng = Cam4Engine(BUNDLE)
    for i in range(5):
        eng.outbox.append({"frame_id": f"r{i}", "sequence_no": i, "captured_at": None,
                           "s": 0.0, "jpeg": b"x"})
    assert [r["frame_id"] for r in eng.take_retained()] == ["r0", "r1"]
    assert len(eng.outbox) == 3


# ---- server wiring --------------------------------------------------------------------------
@pytest.fixture()
def server(tmp_path):
    srv = create_server("127.0.0.1:0", 8_000_000, str(ROOT / "model"), False,
                        str(tmp_path / "store.db"), cam4_bundle_dir=str(BUNDLE))
    port = srv.add_insecure_port("127.0.0.1:0")
    srv.start()
    ch = grpc.insecure_channel(f"127.0.0.1:{port}")
    yield pb2_grpc.InferenceServiceStub(ch), srv
    ch.close()
    srv.stop(None)


def test_both_cameras_share_one_stream_without_disturbing_each_other(server):
    stub, srv = server
    cam1 = grey_frame(value=0)
    cam4 = grey_frame(value=90)
    reqs = []
    for i in range(20):      # interleaved, each camera with its own sequence numbers
        reqs.append(pb2.InferenceRequest(frame_id=f"c1-{i}", camera_id="CAM-1",
                                         image_data=cam1, sequence_no=1000 + i))
        reqs.append(pb2.InferenceRequest(frame_id=f"c4-{i}", camera_id="CAM-4",
                                         image_data=cam4, sequence_no=i + 1))
    resps = list(stub.Infer(iter(reqs)))
    by = {r.frame_id: r for r in resps}
    assert len(resps) == 40
    assert set(by["c1-5"].scalar_measurements) >= {"peak", "dx", "dy", "slot"}
    assert set(by["c4-5"].scalar_measurements) == {"chain_step", "chain_pos",
                                                   "match_quality", "chain_status"}
    assert len(by["c4-5"].detections) == 0
    assert srv.servicer.counters["late_frames_total"] == 0
    assert srv.servicer.cam4.counters["late_frames_total"] == 0
    assert srv.servicer.cam4.counters["frames_total"] == 20


def test_state_is_per_camera(server):
    stub, _ = server
    s1 = stub.GetGodetState(pb2.GodetStateRequest())
    s4 = stub.GetGodetState(pb2.GodetStateRequest(camera_id="CAM-4"))
    assert s1.camera_id == "CAM-1" and s4.camera_id == "CAM-4"
    assert s1.model_version != s4.model_version
    assert s4.health.status == "not_ready"
    with pytest.raises(grpc.RpcError) as err:
        stub.GetGodetState(pb2.GodetStateRequest(camera_id="CAM-9"))
    assert err.value.code() == grpc.StatusCode.INVALID_ARGUMENT


def test_cam4_is_refused_when_not_enabled(tmp_path):
    srv = create_server("127.0.0.1:0", 8_000_000, str(ROOT / "model"), False,
                        str(tmp_path / "store.db"))
    port = srv.add_insecure_port("127.0.0.1:0")
    srv.start()
    ch = grpc.insecure_channel(f"127.0.0.1:{port}")
    try:
        stub = pb2_grpc.InferenceServiceStub(ch)
        with pytest.raises(grpc.RpcError) as err:
            stub.GetGodetState(pb2.GodetStateRequest(camera_id="CAM-4"))
        assert err.value.code() == grpc.StatusCode.INVALID_ARGUMENT
        resps = list(stub.Infer(iter([pb2.InferenceRequest(
            frame_id="x", camera_id="CAM-4", image_data=grey_frame(), sequence_no=1)])))
        assert resps == []                    # dead-lettered like any unknown camera
    finally:
        ch.close()
        srv.stop(None)


# ---- real video -----------------------------------------------------------------------------
@pytest.mark.skipif(not VIDEO.exists(), reason="CAM-4 day video not available")
def test_real_frames_odometer_matches_batch():
    eng = Cam4Engine(BUNDLE)
    steps, quals = [], []
    for i, j in enumerate(video_frames(1500, 600)):
        d, ok = eng.on_frame(j, f"f{i}", i + 1)
        assert ok
        steps.append(d["chain_step"]); quals.append(d["match_quality"])
    assert 7.9 <= float(np.median(steps[1:])) <= 8.3     # batch: 8.08 px/frame
    assert float(np.median(quals[1:])) > 0.5
    assert eng.stream.chain_status() == 0                # moving
    assert len(eng.ring) <= 900
