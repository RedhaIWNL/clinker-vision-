"""Startup self-test gates (plan Phase 4): corrupt bundles refuse loudly, and a
feed with nothing godet-like in view raises template_lost instead of garbage.

Run:  python -m pytest tests/test_selftest.py -q -s
"""
import sys
from pathlib import Path

import numpy as np
import pytest

from support import CAM, ROOT

sys.path.insert(0, str(ROOT / "src" / "gen"))
sys.path.insert(0, str(ROOT))

import inference_v2_pb2 as pb2  # noqa: E402
from src.bundle import BundleError  # noqa: E402
from src.server import RealServicer, create_server  # noqa: E402


def make_servicer(tmp_path):
    import grpc_health.v1.health as health_mod

    return RealServicer(str(ROOT / "model"), 8_000_000,
                        str(tmp_path / "store.db"), health_mod.HealthServicer())


def black_jpeg():
    import cv2

    img = np.zeros((1520, 2688, 3), dtype=np.uint8)
    ok, buf = cv2.imencode(".jpg", img, [cv2.IMWRITE_JPEG_QUALITY, 95])
    assert ok
    return bytes(buf)


def test_missing_bundle_refuses(tmp_path):
    import grpc_health.v1.health as health_mod

    with pytest.raises(BundleError):
        RealServicer(str(tmp_path / "nope"), 8_000_000,
                     str(tmp_path / "s.db"), health_mod.HealthServicer())


def test_create_server_propagates_bundle_error(tmp_path):
    with pytest.raises(BundleError):
        create_server("127.0.0.1:0", 8_000_000, str(tmp_path / "nope"), False,
                      str(tmp_path / "s.db"))


def test_black_feed_raises_template_lost(tmp_path):
    svc = make_servicer(tmp_path)
    jpeg = black_jpeg()
    out = []
    for i in range(120):
        req = pb2.InferenceRequest(frame_id=f"b-{i}", camera_id="CAM-1",
                                   image_data=jpeg, sequence_no=i)
        svc.expected = 0 if svc.expected is None else svc.expected
        if req.sequence_no < svc.expected:
            continue
        assert req.sequence_no == svc.expected
        svc._release(req, out)
        svc.expected += 1
    assert len(out) == 120
    r = svc.GetGodetState(pb2.GodetStateRequest(), None)
    print(f"\nblack feed: status={r.health.status} detail={r.health.detail[:100]}")
    assert r.health.status == "template_lost", r.health.detail
    assert r.health.ready is False
