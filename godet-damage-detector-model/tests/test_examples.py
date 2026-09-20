"""Sample artifacts stay honest: committed JPEGs decode correctly and every
committed expected-response matches batch truth (no video needed).

Run:  python -m pytest tests/test_examples.py -q
"""
import json
import sys
from pathlib import Path

import pandas as pd

from support import CAM, ROOT

sys.path.insert(0, str(ROOT))

FR = ROOT / "examples" / "frames"
RS = ROOT / "examples" / "responses"
FRAMES = {"healthy": 7986, "damage": 2741, "plate": 3844}


def test_frames_decode():
    import cv2
    import numpy as np

    for name in FRAMES:
        img = cv2.imdecode(np.frombuffer((FR / f"{name}.jpg").read_bytes(),
                                         dtype=np.uint8), cv2.IMREAD_COLOR)
        assert img is not None and img.shape == (1520, 2688, 3), name


def test_tier1_match_batch():
    batch = pd.read_csv(CAM / "out" / "h00" / "captures.csv")
    ref = {int(r["frame"]): r for _, r in batch[batch["kind"] == "real"].iterrows()}
    expect = {"healthy": ("readable", False), "damage": ("readable", True),
              "plate": ("occluded", False)}
    for name, frame in FRAMES.items():
        t = json.loads((RS / f"tier1-{name}.json").read_text())
        r = ref[frame]
        sm = t["scalar_measurements"]
        assert abs(sm["lit_L"] - r["lit_L"]) <= 2 and abs(sm["lit_R"] - r["lit_R"]) <= 2
        assert t["status"] == expect[name][0] == r["status"], name
        assert (len(t["detections"]) > 0) == expect[name][1], name
        if t["detections"]:
            d = t["detections"][0]
            assert (d["observation_target"], d["fault_type"]) == ("GODET", "DAMAGE")
            assert set(d["bounding_box"]) == {"x", "y", "width", "height"}


def test_pending_matches_batch():
    batch = pd.read_csv(CAM / "out" / "h00" / "alerts.csv")
    ref = batch[batch["godet_id"] == 224].iloc[0]
    p = json.loads((RS / "pending-224.json").read_text())
    assert (p["kind"], p["godet_id"], p["loop"], p["state"]) == (
        "damage", 224, int(ref["loop"]), "pending")
    assert abs(p["payload"]["lip_now"] - ref["lip_now"]) < 1e-6
    assert p["model_version"].startswith("498184ea+")


def test_health_shape():
    h = json.loads((RS / "health.json").read_text())
    assert h["ready"] is True and h["status"] == "ok"
    assert h["persistent_godets"] == 165 and h["pending"] == 4 and h["confirmed"] == 0
