"""Real-mode wiring: server with --bundle-dir serves Tier-1 from the detector.

Uses a synthetic full-size frame (exercises decode/crop/match/response mapping,
not detection quality — quality is covered by test_replay.py).

Run:  python -m pytest tests/test_server_real.py -q
"""
import sys
from pathlib import Path

import grpc
import numpy as np

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src" / "gen"))
sys.path.insert(0, str(ROOT))

import inference_v2_pb2 as pb2  # noqa: E402
import inference_v2_pb2_grpc as pb2_grpc  # noqa: E402
from src.server import create_server  # noqa: E402


def full_frame_jpeg():
    import cv2

    img = np.zeros((1520, 2688, 3), dtype=np.uint8)
    img[176:176 + 258, 1023:1023 + 144] = 200  # bright ROI so match has signal
    ok, buf = cv2.imencode(".jpg", img, [cv2.IMWRITE_JPEG_QUALITY, 95])
    assert ok
    return bytes(buf)


def test_real_mode_round_trip(tmp_path):
    server = create_server("127.0.0.1:0", 8_000_000, str(ROOT / "model"), False,
                           str(tmp_path / "store.db"))
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    channel = grpc.insecure_channel(f"127.0.0.1:{port}")
    try:
        stub = pb2_grpc.InferenceServiceStub(channel)
        jpeg = full_frame_jpeg()
        reqs = [pb2.InferenceRequest(frame_id=f"f-{i}", camera_id="CAM-1",
                                     image_data=jpeg, sequence_no=i) for i in range(30)]
        resps = list(stub.Infer(iter(reqs)))
        assert len(resps) == 30  # every valid frame gets a Tier-1 response
        r = resps[0]
        assert r.frame_id == "f-0"
        assert "+dev+" in r.model_version or "+thr-v1" in r.model_version
        assert r.scalar_measurements["slot"] == -1  # non-capture: match scalars only
        assert set(r.scalar_measurements) == {"peak", "dx", "dy", "slot"}
        assert len(r.detections) == 0
        st = stub.GetGodetState(pb2.GodetStateRequest())
        # 30 frames << LOOP+300: Tier-2 dark by design (bootstrap needs a loop).
        assert st.health.ready is False
        assert st.health.loop_locked is False
        assert st.health.status == "not_ready"
        assert st.health.counters["frames_total"] == 30
    finally:
        channel.close()
        server.stop(None)
