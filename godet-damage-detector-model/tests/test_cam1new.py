"""New Camera 1 view (re-aimed 2026-09-27): side-plate engine with disc wheels and per-plate outside damage.

Calibrated on 2026-09-27/28 night, morning and evening hours (calibration repo CAM1_NEW_PLAN.md §7:
the user checked the offline judgements on the whole chain, 229 of 230 right, 0 missed). The real-video
tests are skipped when the calibration repo's video is not available; the full check is a replay
(tools/cam4_replay.py) compared with the offline alert places.

Run:  python -m pytest tests/test_cam1new.py -q
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
from src.cam4.plates import best_joints, valley_score  # noqa: E402

BUNDLE = ROOT / "model" / "cam1new"
NIGHT = CAM / "data" / "camera1newnight" / "NVR_ch1_main_20260928000000_20260928010000.mp4"


def video_frames(path, start_frame, n):
    import cv2

    cap = cv2.VideoCapture(str(path)); cap.set(cv2.CAP_PROP_POS_FRAMES, start_frame)
    for _ in range(n):
        ok, f = cap.read()
        if not ok:
            break
        yield cv2.imencode(".jpg", f, [cv2.IMWRITE_JPEG_QUALITY, 85])[1].tobytes()
    cap.release()


def test_bundle_is_the_new_camera_1():
    b = load_cam4_bundle(BUNDLE)
    assert b.camera_id == "CAM-1" and b.det["mode"] == "plates"
    assert len(b.plate_models) == 3 and b.plates["stack"]["threshold"] > 0
    assert b.wheels["mode"] == "disc" and b.wheel_template is not None
    assert b.ident["godets"] == 1210 and b.chainmap.shape == (5, b.ident["loop_cols"])


def test_joints_are_chosen_together():
    """Evenly spaced strong edges, one missing: the missing joint is bridged, not lost."""
    e = np.ones(3000, np.float32)
    true = list(range(100, 2900, 190))
    for j in true:
        if j != 100 + 5 * 190:
            e[j - 2:j + 3] = 10.0
    js = best_joints(e, 100, 150, 235, 190.5, 0.004, 4.0)
    assert all(min(abs(j - t) for j in js) <= 3 for t in true[1:-1])


def test_valley_finds_a_thin_dark_line_not_a_step():
    g = np.full((140, 190), 150.0, np.float32)
    step = g.copy(); step[70:] = 90.0                      # a fold: bright above, dark below
    cut = g.copy(); cut[70:73] = 60.0                      # a cut: dark line, bright on both sides
    assert valley_score(cut) > 5 * max(valley_score(step), 1e-3)


@pytest.mark.skipif(not NIGHT.exists(), reason="new Camera 1 night video not available")
def test_night_video_locks_and_judges_plates():
    eng = Cam4Engine(BUNDLE)
    for i, j in enumerate(video_frames(NIGHT, 45000, 4000)):
        eng.on_frame(j, f"f{i}", i + 1)
    assert eng.ident.locked
    assert eng.plates.counters["plates_total"] > 100            # ~1.25 plates per second after the lock (~40 s), minus the newest waiting for the map (measured 140)
    assert eng.wheels.counters["wheels_total"] > 30
    d = next(iter(eng.plates.g.values()))
    assert all(np.isfinite(d["passes"][0][:4]))
