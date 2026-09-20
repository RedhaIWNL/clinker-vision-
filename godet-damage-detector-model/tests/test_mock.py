"""Phase 1 gate: mock Tier-1 round trip, input policy, determinism.

Run:  python -m pytest tests/ -q   (from clinker-vision-model/, venv active)
"""
import sys
from pathlib import Path

import grpc
import numpy as np

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src" / "gen"))  # protoc stubs import each other top-level
sys.path.insert(0, str(ROOT))

from src import mock as mock_mod  # noqa: E402
import inference_v2_pb2 as pb2  # noqa: E402
import inference_v2_pb2_grpc as pb2_grpc  # noqa: E402
from src.server import create_server  # noqa: E402


def make_jpeg(seed: int = 0) -> bytes:
    import cv2

    rng = np.random.default_rng(seed)
    img = rng.integers(0, 255, (64, 64, 3), dtype=np.uint8)
    ok, buf = cv2.imencode(".jpg", img)
    assert ok
    return bytes(buf)


def live_stub():
    server = create_server("127.0.0.1:0", 8_000_000, None, True)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    channel = grpc.insecure_channel(f"127.0.0.1:{port}")
    stub = pb2_grpc.InferenceServiceStub(channel)
    return server, channel, stub


def test_round_trip_echo_and_shape():
    server, channel, stub = live_stub()
    try:
        jpeg = make_jpeg()
        req = pb2.InferenceRequest(frame_id="f-1", camera_id="CAM-1",
                                   image_data=jpeg, sequence_no=7)
        resps = list(stub.Infer(iter([req])))
        assert len(resps) == 1
        r = resps[0]
        assert r.frame_id == "f-1"  # echoed exactly
        assert r.model_version == "mock-0"
        assert set(r.scalar_measurements) == {
            "lit_L", "lit_R", "peak", "dx", "dy", "slot", "status_code"}
        assert len(r.detections) == 1
        d = r.detections[0]
        assert (d.observation_target, d.fault_type) == ("GODET", "DAMAGE")
        # proto float32: exact bitwise equality does not survive the wire.
        # Plan tolerance policy: floats ±1e-3 unless stated; box states ±1e-6.
        import pytest

        assert (d.bounding_box.x, d.bounding_box.y,
                d.bounding_box.width, d.bounding_box.height) == pytest.approx(
            (mock_mod.FIXED_BOX["x"], mock_mod.FIXED_BOX["y"],
             mock_mod.FIXED_BOX["width"], mock_mod.FIXED_BOX["height"]), abs=1e-6)
        assert r.processed_at.seconds > 0
    finally:
        channel.close()
        server.stop(None)


def test_invalid_jpeg_dead_lettered_stream_survives():
    server, channel, stub = live_stub()
    try:
        good = pb2.InferenceRequest(frame_id="good", camera_id="CAM-1",
                                    image_data=make_jpeg(1), sequence_no=1)
        bad = pb2.InferenceRequest(frame_id="bad", camera_id="CAM-1",
                                   image_data=b"not-a-jpeg", sequence_no=2)
        resps = list(stub.Infer(iter([good, bad, good])))
        # bad frame: no response, stream stays up -> 2 responses, ids intact.
        assert [r.frame_id for r in resps] == ["good", "good"]
        assert server.servicer.dead_letters_total == 1
    finally:
        channel.close()
        server.stop(None)


def test_unknown_camera_dead_lettered():
    server, channel, stub = live_stub()
    try:
        req = pb2.InferenceRequest(frame_id="x", camera_id="CAM-9",
                                   image_data=make_jpeg(), sequence_no=1)
        assert list(stub.Infer(iter([req]))) == []
        assert server.servicer.dead_letters_total == 1
    finally:
        channel.close()
        server.stop(None)


def test_mock_deterministic():
    jpeg = make_jpeg(3)
    a = mock_mod.mock_scalars(jpeg)
    b = mock_mod.mock_scalars(jpeg)
    assert a == b
