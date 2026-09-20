"""Throughput bench (plan INF-4, dense numbers): 8 MP JPEG decode rate and
end-to-end Tier-1 latency on real night frames. Gates: decode >= 50 fps,
Tier-1 p99 < 40 ms (one frame budget at 25 fps), sustained >= 25 fps.

Measured 2026-09-20 (dev machine): decode 83/s, Tier-1 p50 12.9 ms /
p99 15.7 ms / max 16.0 ms. Re-record here on release hardware.

Run:  python -m pytest tests/test_bench.py -q -s
"""
import sys
import time
from pathlib import Path

import numpy as np

from support import CAM, ROOT, encode, read_video_frames

sys.path.insert(0, str(ROOT))

from src.bundle import load_bundle  # noqa: E402
from src.pipeline import StreamProcessor  # noqa: E402

N = 60


def test_bench():
    frames = read_video_frames(40000, N)
    t0 = time.time()
    blobs = encode(frames, "jpg", 95)
    enc_rate = N / (time.time() - t0)

    import cv2

    t0 = time.time()
    for b in blobs:
        cv2.imdecode(np.frombuffer(b, dtype=np.uint8), cv2.IMREAD_GRAYSCALE)
    dec_rate = N / (time.time() - t0)

    p = StreamProcessor(load_bundle(ROOT / "model"))
    lats = []
    for i, b in enumerate(blobs):
        t = time.time()
        p.on_frame(b, frame_id=f"bench-{i}", sequence_no=i)
        lats.append((time.time() - t) * 1000)
    lats = np.array(lats)
    p50, p99 = float(np.median(lats)), float(np.percentile(lats, 99))
    print(f"\nbench: jpeg encode {enc_rate:.0f}/s decode {dec_rate:.0f}/s | "
          f"tier1 p50 {p50:.1f}ms p99 {p99:.1f}ms max {lats.max():.1f}ms "
          f"({1000 / p50:.0f} fps sustained)")
    assert dec_rate >= 50, f"decode {dec_rate:.0f}/s < 50"
    assert p99 < 40, f"tier1 p99 {p99:.1f}ms exceeds the 40 ms frame budget"
    assert 1000 / p50 >= 25, "tier1 sustained rate below 25 fps"
