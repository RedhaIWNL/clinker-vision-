"""Real-detector fixtures (plan matrix #1-3): healthy / damage / no-detection.

Each fixture feeds a ±15-frame sequence around a batch-known capture (single
frames never trigger the phase-lock) and asserts on that capture's Tier-1.
Batch truth (out/h00/captures.csv): frame 7986 healthy (lit 150/136),
frame 2741 short right lip (lit 150/100), frame 3844 separator plate.

Run:  python -m pytest tests/test_fixtures.py -q -s
"""
import sys
from pathlib import Path

from support import CAM, ROOT, encode, read_video_frames

sys.path.insert(0, str(ROOT))

from src.bundle import load_bundle  # noqa: E402
from src.pipeline import StreamProcessor  # noqa: E402

CASES = {
    # name: (target frame, half-window, lead-in frames for stream warmup)
    "healthy": (7986, 15, 0),
    "damage": (2741, 15, 0),
    "plate": (3844, 15, 1500),  # plate rule needs ~50 captures of history
}


def run_case(name, codec="jpg", quality=95):
    # JPEG throughout: PNG-encoding 1500 full frames is prohibitively slow and
    # every assert below tolerates JPEG drift (±2 rows; thresholds have margin).
    target, half, lead = CASES[name]
    start = target - half - lead
    frames = read_video_frames(start, lead + 2 * half + 1)
    blobs = encode(frames, codec, quality)
    p = StreamProcessor(load_bundle(ROOT / "model"))
    caps = {}
    for k, b in enumerate(blobs):
        d, ok = p.on_frame(b, frame_id=f"{name}-{k}", sequence_no=k)
        assert ok
        if d["is_capture"]:
            caps[d["frame"]] = d
    # capture frame is stream-relative; target sits at index lead+half
    want = lead + half
    got = caps.get(want)
    if got is None:  # nearer-zero tie may pick the neighbor (documented ±1)
        got = caps.get(want - 1, caps.get(want + 1))
    assert got is not None, f"no capture near {name} frame"
    return got


def test_healthy():
    d = run_case("healthy")
    assert d["status"] == "readable", d["status"]
    assert d["indicator"] is False
    assert d["lit_L"] >= 123 and d["lit_R"] >= 112, (d["lit_L"], d["lit_R"])
    print(f"\nhealthy: status=readable lit={d['lit_L']}/{d['lit_R']} no indicator")


def test_damage():
    d = run_case("damage")
    assert d["status"] == "readable", d["status"]
    assert d["indicator"] is True
    assert d["lit_R"] < 112, d["lit_R"]
    print(f"\ndamage: status=readable lit={d['lit_L']}/{d['lit_R']} SHORT-R indicator")


def test_no_detection():
    d = run_case("plate")
    assert d["status"] in ("occluded", "edge", "weak"), d["status"]
    assert d["indicator"] is False
    print(f"\nplate: status={d['status']} no indicator, no alert row")


def test_jpeg_within_tolerance():
    import pandas as pd

    ref = pd.read_csv(CAM / "out" / "h00" / "captures.csv")
    ref = {int(r["frame"]): (int(r["lit_L"]), int(r["lit_R"]))
           for _, r in ref[ref["kind"] == "real"].iterrows()}
    worst = 0
    for name, (target, _, _) in CASES.items():
        d = run_case(name, "jpg")
        r = ref[target]
        worst = max(worst, abs(int(d["lit_L"]) - r[0]), abs(int(d["lit_R"]) - r[1]))
    print(f"\nJPEG-q95 worst lit drift on fixtures: {worst} rows")
    assert worst <= 2, "JPEG drift exceeds ±2 rows"


def test_deterministic():
    a = run_case("damage")
    b = run_case("damage")
    assert a == b, "same JPEGs must give byte-identical Tier-1 dicts"
