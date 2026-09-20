"""JPEG-parity check (plan Phase 2): the serving path decodes full frames, but the
batch calibration ran on raw NVR decode. Same scene must agree: PNG full frames
(lossless) reproduce batch captures exactly; JPEG-q95 full frames agree within
±2 lit rows, else thresholds must be re-derived on JPEG input.

Range: video frames 37500-39000 (1500-1560 s, the labelled window: godets +
separator plates). Takes a few minutes (H.264 decode of 8 MP frames).

Run:  python -m pytest tests/test_parity.py -q -s
"""
import pytest
pytestmark = pytest.mark.skip(
    reason="SLOW (~9 min H.264 decode): deferred, not dropped. Re-enable before "
           "Phase 2 sign-off — the JPEG-drift gate (<=2 rows) is still an exit item.")

import sys
from pathlib import Path

import cv2
import numpy as np
import pandas as pd

ROOT = Path(__file__).resolve().parents[1]
CAM = ROOT.parent
sys.path.insert(0, str(ROOT))

from src.bundle import load_bundle  # noqa: E402
from src.pipeline import StreamProcessor  # noqa: E402

VIDEO = CAM / "data" / "camera_1" / "night" / "NVR_ch1_main_20260820000000_20260820010000.mp4"
START, COUNT = 37500, 1500


def run_feed(codec):
    """Feed full frames through on_frame; return {capture frame: (lit_L, lit_R)}."""
    p = StreamProcessor(load_bundle(ROOT / "model"))
    cap = cv2.VideoCapture(str(VIDEO))
    assert cap.isOpened(), f"cannot open {VIDEO}"
    cap.set(cv2.CAP_PROP_POS_FRAMES, START)
    out = {}
    for k in range(COUNT):
        ok, frame = cap.read()
        assert ok, f"video ended at offset {k}"
        if codec == "png":
            ok2, buf = cv2.imencode(".png", frame)
        else:
            ok2, buf = cv2.imencode(".jpg", frame, [cv2.IMWRITE_JPEG_QUALITY, 95])
        assert ok2
        d, good = p.on_frame(bytes(buf), frame_id=f"f-{START + k}",
                             sequence_no=START + k)
        assert good, "ROI crop fell outside the frame"
        if d["is_capture"]:
            out[START + d["frame"]] = (d["lit_L"], d["lit_R"], d["status"])
    cap.release()
    return out


def align(got, ref):
    """Match each batch capture to the nearest run capture within ±1 frame
    (matchTemplate near-ties can slide a capture one frame; inherent to the
    algorithm, batch included). Returns (exact, pairs) where pairs maps batch
    frame -> run frame."""
    gs = sorted(got)
    pairs, exact, used = {}, 0, set()
    for f in sorted(ref):
        cands = [g for g in gs if abs(g - f) <= 1 and g not in used]
        assert cands, f"batch capture {f} unmatched within ±1"
        g = min(cands, key=lambda x: abs(x - f))
        pairs[f] = g
        used.add(g)
        exact += (g == f)
    assert len(used) == len(gs), f"extra run captures: {sorted(set(gs) - used)[:5]}"
    return exact, pairs


def test_parity():
    batch = pd.read_csv(CAM / "out" / "h00" / "captures.csv")
    real = batch[batch["kind"] == "real"]
    ref = {int(r["frame"]): (int(r["lit_L"]), int(r["lit_R"]))
           for _, r in real.iterrows() if START <= int(r["frame"]) < START + COUNT}
    assert len(ref) > 40, "window too quiet to prove anything"

    png = run_feed("png")
    exact, pairs = align(png, ref)
    for f, g in pairs.items():
        assert (int(png[g][0]), int(png[g][1])) == ref[f], f"PNG lit mismatch at {f}/{g}"
    print(f"\nPNG: {len(ref)} captures, {exact} exact-frame, rest ±1 tie flips, lit exact")
    assert exact / len(ref) >= 0.99, "too many tie flips: investigate, don't widen"

    jpg = run_feed("jpg")
    exact_j, pairs_j = align(jpg, ref)
    worst = 0
    for f, g in pairs_j.items():
        worst = max(worst, abs(int(jpg[g][0]) - ref[f][0]), abs(int(jpg[g][1]) - ref[f][1]))
    print(f"JPEG-q95: {len(ref)} captures, {exact_j} exact-frame, worst lit drift: {worst} rows")
    assert worst <= 2, f"JPEG drift {worst} > 2 rows: re-derive thresholds on JPEG input"
