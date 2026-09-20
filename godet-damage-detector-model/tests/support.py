"""Shared test helpers: rebuild SlotRecords from a batch captures.csv so Tier-2
logic is testable without video decode (fast). t_wall is synthetic but
monotonic (slot * 0.8 s): health rate rules see a well-formed stream.
"""
import sys
from pathlib import Path

import numpy as np
import pandas as pd

ROOT = Path(__file__).resolve().parents[1]
CAM = ROOT.parent
sys.path.insert(0, str(ROOT))

from src.pipeline import SlotRecord  # noqa: E402

H00_VIDEO = CAM / "data" / "camera_1" / "night" / "NVR_ch1_main_20260820000000_20260820010000.mp4"


def read_video_frames(start, count):
    """Raw BGR full frames from the h00 video (deterministic decode)."""
    import cv2

    cap = cv2.VideoCapture(str(H00_VIDEO))
    assert cap.isOpened(), f"cannot open {H00_VIDEO}"
    cap.set(cv2.CAP_PROP_POS_FRAMES, start)
    frames = []
    for _ in range(count):
        ok, f = cap.read()
        assert ok, "video ended early"
        frames.append(f)
    cap.release()
    return frames


def encode(frames, codec="jpg", quality=95):
    import cv2

    out = []
    for f in frames:
        if codec == "jpg":
            ok, buf = cv2.imencode(".jpg", f, [cv2.IMWRITE_JPEG_QUALITY, quality])
        else:
            ok, buf = cv2.imencode(".png", f)
        assert ok
        out.append(bytes(buf))
    return out


def black_full_jpeg():
    import cv2

    img = np.zeros((1520, 2688, 3), dtype=np.uint8)
    ok, buf = cv2.imencode(".jpg", img, [cv2.IMWRITE_JPEG_QUALITY, 95])
    assert ok
    return bytes(buf)

FLOAT_COLS = ("dx", "dy", "peak", "lit_L", "lit_R", "top_L", "bot_L", "bright_L",
              "straight_L", "slope_L", "top_R", "bot_R", "bright_R", "straight_R",
              "slope_R", "face_resid")


def slots_from_captures(csv_path):
    df = pd.read_csv(csv_path).sort_values("slot").reset_index(drop=True)
    recs = []
    for _, r in df.iterrows():
        kw = {"slot": int(r["slot"]), "kind": r["kind"],
              "frame_id": f"f-{int(r['frame'])}", "sequence_no": int(r["frame"]),
              "status": r["status"],
              "short_L": bool(r["short_L"]), "short_R": bool(r["short_R"]),
              "t_wall": int(r["slot"]) * 0.8}
        for c in FLOAT_COLS:
            v = r[c]
            kw[c] = float(v) if not (isinstance(v, float) and np.isnan(v)) else np.nan
        recs.append(SlotRecord(**kw))
    return recs
