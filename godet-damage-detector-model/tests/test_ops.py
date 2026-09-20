"""Phase 5 gates: oversized-JPEG policy, per-frame deadline, shutdown snapshot.

Run:  python -m pytest tests/test_ops.py -q -s
"""
import sys
import time
from pathlib import Path

import grpc
import numpy as np

from support import CAM, ROOT

sys.path.insert(0, str(ROOT / "src" / "gen"))
sys.path.insert(0, str(ROOT))

import inference_v2_pb2 as pb2  # noqa: E402
import inference_v2_pb2_grpc as pb2_grpc  # noqa: E402
from src.server import RealServicer, create_server  # noqa: E402


def small_jpeg():
    import cv2

    img = np.zeros((64, 64, 3), dtype=np.uint8)
    ok, buf = cv2.imencode(".jpg", img)
    assert ok
    return bytes(buf)


def test_oversized_dead_lettered_stream_survives():
    server = create_server("127.0.0.1:0", 10, None, True)  # max 10 bytes: all oversized
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    channel = grpc.insecure_channel(f"127.0.0.1:{port}")
    try:
        stub = pb2_grpc.InferenceServiceStub(channel)
        reqs = [pb2.InferenceRequest(frame_id=f"o-{i}", camera_id="CAM-1",
                                     image_data=small_jpeg(), sequence_no=i)
                for i in range(3)]
        assert list(stub.Infer(iter(reqs))) == []
        assert server.servicer.dead_letters_total == 3
    finally:
        channel.close()
        server.stop(None)


def test_frame_deadline_counts_and_skips(tmp_path):
    import grpc_health.v1.health as health_mod

    svc = RealServicer(str(ROOT / "model"), 8_000_000, str(tmp_path / "s.db"),
                       health_mod.HealthServicer(), frame_deadline_s=0.0)
    import cv2

    img = np.zeros((1520, 2688, 3), dtype=np.uint8)
    ok, buf = cv2.imencode(".jpg", img, [cv2.IMWRITE_JPEG_QUALITY, 95])
    assert ok
    out = []
    req = pb2.InferenceRequest(frame_id="d-0", camera_id="CAM-1",
                               image_data=bytes(buf), sequence_no=0)
    svc.expected = 0
    svc._release(req, out)
    assert out == [], "over-deadline frame must produce no response"
    assert svc.counters["deadline_exceeded_total"] == 1
    print("\ndeadline path: counted, skipped, sequence consumed")


def test_shutdown_persist_roundtrip(tmp_path):
    import grpc_health.v1.health as health_mod

    svc = RealServicer(str(ROOT / "model"), 8_000_000, str(tmp_path / "s.db"),
                       health_mod.HealthServicer())
    before = time.time()
    svc._persist()  # nothing processed yet: must still write a loadable snapshot
    assert (tmp_path / "s.db").exists()
    from src.store import Store

    assert Store(str(tmp_path / "s.db")).load(svc.bundle.version) is not None
    assert time.time() - before < 5, "empty persist must be instant"
