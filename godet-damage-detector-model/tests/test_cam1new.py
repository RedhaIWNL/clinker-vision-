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


class FakeIdentity:
    """Locked; map position = column. Anchors trail the newest column by 2000 (a piece)."""
    def __init__(self):
        from collections import deque
        self.locked = True
        self.anchors = deque(maxlen=16)
        self.loop_base, self.loop_offset = 0, 0

    def advance(self, newest_col):
        c = newest_col - 2000
        if not self.anchors or c > self.anchors[-1][0]:
            self.anchors.append((c, float(c)))


def test_plates_go_on_after_a_stream_restart():
    """2026-09-29 replay: after the second video (a sequence restart: the stream numbers columns from 0
    again) not one plate was scored; plates of the old numbering waited for their crop forever."""
    from src.cam4.plates import PlateTracker
    b = load_cam4_bundle(BUNDLE)
    t = PlateTracker(b, FakeIdentity(), "CAM-1")
    g = np.full((402, 1000), 120.0, np.float32)
    g[:, ::190] = 250.0                                         # a plate joint every 190 columns

    def run(first, n):
        for c0 in range(first, first + n, 1000):
            t.ident.advance(c0 + 999)
            t.on_strip(np.arange(c0, c0 + 1000), np.roll(g, -(c0 % 190), 1), 0)

    run(1_000_000, 12000)
    before = t.counters["plates_total"]
    assert before > 20 and t.pending                            # plates still waiting when the stream restarts
    t.ident = FakeIdentity()                                    # the engine rebuilds identity on a restart
    run(0, 12000)
    assert t.counters["plates_total"] >= before + 20


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


def test_lost_pictures_leave_no_empty_columns():
    """2026-10-01: a lost picture left ~5 empty columns that the plate cutter took for joints (false
    "out of line"). The next picture's slit now reaches back to the previous one, whatever the jump."""
    from src.cam4.stream import Cam4Stream, MAX_SLIT_HALF
    st = Cam4Stream(load_cam4_bundle(BUNDLE))
    s, h = 1000.0, 6
    for step in [9.5, 9.5, 19.0, 9.5, 28.4, 12.3, 38.0, 9.4, 47.5, 9.5, 9.5]:   # 0-4 pictures lost
        st.s_pasted, st.h_pasted = s, h
        A = st._slit(s + step)[0]
        h_new = min(-A[0], A[-1])
        assert (s + step) - h_new <= s + h + 1, (step, h, h_new)      # reaches back to the previous slit
        if step < 12.0:
            assert len(A) == len(st.A)                                 # nothing lost: the calibrated slit
        s, h = s + step, h_new
    st.s_pasted, st.h_pasted = 0.0, 6
    assert min(-st._slit(500.0)[0][0], st._slit(500.0)[0][-1]) == MAX_SLIT_HALF   # a wild jump stays bounded


def test_plate_with_empty_columns_is_not_judged():
    """Safety net: empty columns are never a joint, and a plate that has some is left to the next loop."""
    from src.cam4.plates import PlateTracker
    from src.cam4.identity import Cam4Identity
    b = load_cam4_bundle(BUNDLE)
    t = PlateTracker(b, Cam4Identity(b), "CAM-1")
    t._position = lambda col: float(col)
    rng = np.random.default_rng(0)
    g = np.tile(rng.uniform(60, 200, (402, 190)).astype(np.float32), (1, 40))
    g[:, ::190] = 20.0                                                 # joints every 190 columns
    miss = np.zeros(g.shape[1], bool)
    miss[3000:3006] = True; g[:, 3000:3006] = 0.0                      # one lost picture
    t.on_strip(np.arange(g.shape[1]), g, 0, miss)
    assert t.counters["plates_skipped_missing_total"] >= 1
    assert t.counters["plates_total"] >= 20


def test_confirmed_godet_keeps_and_resends_its_own_picture():
    """2026-10-01: alerts showed the live view (another godet) when the pipeline's picture cache had been
    emptied by a lane restart. The godet keeps its strongest frame in RAM and hands it over again when it is
    confirmed; after a model restart it gets a fresh one on its next pass."""
    from src.cam4.plates import PlateTracker
    from src.cam4.identity import Cam4Identity
    b = load_cam4_bundle(BUNDLE)
    sent = []
    def retain(col):
        return {"frame_id": f"f{col}", "box": [0, 0, 1, 1], "captured_at": None,
                "_frame": {"frame_id": f"f{col}", "jpeg": b"jpeg", "s": float(col)}}
    t = PlateTracker(b, Cam4Identity(b), "CAM-1", retain, lambda rec: sent.append(rec["frame_id"]))
    bad, good = np.array([0.99, 0.99, 0.99]), np.array([0.01, 0.01, 0.01])
    t._on_plate(7, 0, bad, 0.0, 100)
    assert not sent and 7 in t.frames                      # suspicious: picture kept, not confirmed yet
    t._on_plate(7, 1, bad, 0.0, 200)
    d = t.g[7]
    assert d["state"] == "confirmed" and sent == [d["evidence"]["frame_id"]] and d["sent"] == sent[-1]
    t._on_plate(8, 0, good, 0.0, 300)
    assert 8 not in t.frames                                # a normal godet keeps no picture
    t2 = PlateTracker(b, Cam4Identity(b), "CAM-1", retain, lambda rec: sent.append(rec["frame_id"]))
    t2.restore(t.snapshot())                                # model restart: the JPEGs are gone
    assert t2.g[7]["sent"] is None and not t2.frames
    t2._on_plate(7, 2, good, 0.0, 400)                      # next pass, even a mild one: fresh picture
    assert t2.g[7]["sent"] == "f400" and sent[-1] == "f400"
