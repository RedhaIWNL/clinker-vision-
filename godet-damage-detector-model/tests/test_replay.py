"""Phase 2 gate: stream out/h00 ROI memmap through StreamProcessor.on_roi and
reproduce the batch captures.csv measurements.

Tolerances (plan): capture frame set identical; lit ints exact; peak/dx/dy
±1e-3; every status difference must be in the allowed classes (edge-before
retro-mark, trailing-median plate boundary) — the strong invariant is zero
UNEXPLAINED diffs. Raw agreement sits ~88% because ~12% of captures neighbor a
separator plate (edge zones); that is expected, not a failure.

Run:  python -m pytest tests/test_replay.py -q   (takes a few minutes)
"""
import sys
import time
from pathlib import Path

import numpy as np
import pandas as pd

ROOT = Path(__file__).resolve().parents[1]
CAM = ROOT.parent
sys.path.insert(0, str(ROOT))

from src.bundle import load_bundle  # noqa: E402
from src.pipeline import StreamProcessor  # noqa: E402

H00 = CAM / "out" / "h00"
ALLOWED_MISMATCH = (
    lambda mine, ref: (ref == "edge" and mine in ("readable", "weak")) or
                      (ref == "occluded" and mine == "weak"))


def test_replay_h00():
    R = np.load(H00 / "roi_all.npy", mmap_mode="r")
    n = int(json_n())
    batch = pd.read_csv(H00 / "captures.csv")
    real = batch[batch["kind"] == "real"].reset_index(drop=True)

    p = StreamProcessor(load_bundle(ROOT / "model"))
    got = []  # (capture frame, tier1)
    t0 = time.time()
    for i in range(n):
        d = p.on_roi(np.ascontiguousarray(R[i]), frame_id=f"f-{i}", sequence_no=i)
        if d["is_capture"]:
            got.append((d["frame"], d))
    el = time.time() - t0
    fps = n / el
    print(f"\nreplay: {n} frames in {el:.0f}s = {fps:.0f} fps, "
          f"{len(got)} captures vs batch {len(real)}")

    assert fps >= 100, f"throughput gate: {fps:.0f} fps < 100"
    assert len(got) == len(real), f"capture count {len(got)} != batch {len(real)}"

    gf = np.array([g[0] for g in got])
    bf = real["frame"].values
    assert (gf == bf).all(), f"capture frames differ: first at {np.nonzero(gf != bf)[0][:5]}"

    bad_lit = 0
    bad_float = 0
    mism = []
    for (i, d), (_, r) in zip(got, real.iterrows()):
        if int(d["lit_L"]) != int(r["lit_L"]) or int(d["lit_R"]) != int(r["lit_R"]):
            bad_lit += 1
        if abs(d["peak"] - r["peak"]) > 1e-3 or d["dx"] != r["dx"] or d["dy"] != r["dy"]:
            bad_float += 1
        if d["status"] != r["status"]:
            if ALLOWED_MISMATCH(d["status"], r["status"]):
                mism.append((i, d["status"], r["status"]))
            else:
                mism.append((i, d["status"], r["status"], "UNEXPLAINED"))
    unexplained = [m for m in mism if len(m) == 4]
    agree = 1 - len(mism) / len(got)
    print(f"lit exact: {len(got)-bad_lit}/{len(got)}  floats ok: {len(got)-bad_float}/{len(got)}  "
          f"status agreement: {100*agree:.2f}% ({len(mism)} diffs, {len(unexplained)} unexplained)")
    assert bad_lit == 0, f"lit mismatches: {bad_lit}"
    assert bad_float == 0, f"peak/dx/dy mismatches: {bad_float}"
    assert not unexplained, f"unexplained status diffs (first 10): {unexplained[:10]}"
    assert agree >= 0.85, f"status agreement {agree:.3f} < 0.85"


def json_n():
    import json

    return json.loads((H00 / "meta.json").read_text())["n"]
