"""CAM-3 (day side plates, the mirror of CAM-4): its own bundle and detector mode.

Calibrated 2026-09-28 on the 2026-09-27 09:59-12:26 recording (calibration repo CAM3_DAY_PLAN.md
section 6): plate-vs-neighbours score on a darkness map, image-band fingerprint (5 channels),
multi-scale chain tracking, empty-conveyor guard. The real-video tests are skipped when the
calibration repo's video is not available; the full-recording gate is tools/cam4_replay.py.

Run:  python -m pytest tests/test_cam3.py -q
"""
import os
import sys
from pathlib import Path

import numpy as np
import pytest

ROOT = Path(__file__).resolve().parents[1]
CAM = Path(os.environ.get("CLINKER_CALIB_ROOT", ROOT.parent))
sys.path.insert(0, str(ROOT / "src" / "gen"))
sys.path.insert(0, str(ROOT))

from src.cam4.bundle import load_cam4_bundle  # noqa: E402
from src.cam4.engine import Cam4Engine  # noqa: E402

BUNDLE = ROOT / "model" / "cam3"
VIDEO = CAM / "data" / "camera_3" / "day" / "NVR_ch3_main_20260927095948_20260927122621.mp4"
START = 9 * 3600 + 59 * 60 + 48                       # the video starts at 09:59:48


def video_frames(clock, n):
    """n frames from the given clock time (seconds since midnight), MJPEG-encoded as the pipeline does."""
    import cv2

    cap = cv2.VideoCapture(str(VIDEO))
    cap.set(cv2.CAP_PROP_POS_FRAMES, int((clock - START) * 25))
    for _ in range(n):
        ok, f = cap.read()
        if not ok:
            break
        ok, buf = cv2.imencode(".jpg", f, [cv2.IMWRITE_JPEG_QUALITY, 85])
        yield buf.tobytes()
    cap.release()


def test_bundle_is_camera_3_mirrored_with_band_fingerprint():
    b = load_cam4_bundle(BUNDLE)
    assert b.camera_id == "CAM-3"
    assert b.v[0] < 0                                  # rail toward -x: the mirror of CAM-4
    assert b.chainmap.shape == (5, b.ident["loop_cols"]) and b.ident["godets"] == 1210
    assert b.det["mode"] == "neighbours" and b.strip_rows[0] == 20 and b.strip_rows[1] >= 340
    assert load_cam4_bundle(ROOT / "model" / "cam4").v[0] > 0   # CAM-4 unchanged


@pytest.mark.skipif(not VIDEO.exists(), reason="CAM-3 day video not available")
def test_loaded_chain_locks_and_is_judged():
    eng = Cam4Engine(BUNDLE)
    steps, quals = [], []
    for i, j in enumerate(video_frames(10 * 3600 + 5 * 60, 2500)):      # 10:05, loaded
        d, ok = eng.on_frame(j, f"f{i}", i + 1)
        assert ok
        steps.append(d["chain_step"]); quals.append(d["match_quality"])
    assert 5.2 <= float(np.median(steps[1:])) <= 6.0     # batch: 5.5-5.8 px/frame
    assert float(np.median(quals[1:])) > 0.5              # batch: 0.60
    assert eng.ident.locked                               # locks in about a minute
    assert eng.stream.counters["empty_columns_total"] == 0
    assert eng.ident.counters["passes_total"] > 50        # godets are being judged


@pytest.mark.skipif(not VIDEO.exists(), reason="CAM-3 day video not available")
def test_empty_conveyor_is_reported_and_not_judged():
    eng = Cam4Engine(BUNDLE)
    for i, j in enumerate(video_frames(12 * 3600 + 8 * 60, 1500)):       # 12:08, empty godets
        eng.on_frame(j, f"f{i}", i + 1)
    s = eng.stream
    assert s.counters["empty_columns_total"] > 0.8 * (s.done - 1100)
    assert s.empty_share() > 0.8
    assert eng.ident.counters["passes_total"] == 0        # nothing judged on an empty conveyor
    import inference_v2_pb2 as pb2
    h = pb2.GodetStateResponse().health
    eng.fill_health(h)
    assert "conveyor empty" in h.detail


def test_bundle_has_disc_wheels():
    b = load_cam4_bundle(BUNDLE)
    assert b.wheels["mode"] == "disc" and b.wheel_template is not None
    assert b.strip_rows[1] >= b.wheels["rows"][1] + b.wheels["play"]      # the stream keeps the wheel band


@pytest.mark.skipif(not VIDEO.exists(), reason="CAM-3 day video not available")
def test_wheels_are_found_on_real_video():
    """2026-09-27 10:05: wheels ~1 per 3.9 godets (offline 2,637 in 146 min; user check 81/81, 0 false).
    Measured from the lock over ~45 godets, where the local rate varies with the 3/4/5-godet spacing
    (measured 14 wheels = 0.32 per godet); the full-recording replay is the precise check."""
    eng = Cam4Engine(BUNDLE); lock_col = None
    for i, j in enumerate(video_frames(10 * 3600 + 5 * 60, 3000)):
        eng.on_frame(j, f"f{i}", i + 1)
        if lock_col is None and eng.ident.locked:
            lock_col = eng.stream.done
    placed = eng.wheels.counters["wheels_total"]
    godets = (eng.ident.anchors[-1][0] - lock_col) / eng.b.ident["pitch"]
    assert placed >= 8 and 0.15 <= placed / godets <= 0.45
    assert eng.wheels.counters["wheels_unplaced_total"] == 0
