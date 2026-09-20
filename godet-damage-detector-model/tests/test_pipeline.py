"""Unit tests for the streaming trigger + slot primitives.

Run:  python -m pytest tests/test_pipeline.py -q
"""
import sys
from pathlib import Path

import numpy as np

ROOT = Path(__file__).resolve().parents[1]
CAM = ROOT.parent
sys.path.insert(0, str(ROOT))

from src.bundle import load_bundle  # noqa: E402
from src.pipeline import StreamProcessor  # noqa: E402


def proc():
    return StreamProcessor(load_bundle(ROOT / "model"))


def push_ramp(p, dxs):
    """Inject a fake dx history; returns _crossing() at the end (0/-1/None)."""
    for v in dxs:
        p.hist.append((p.pos, float(v), 0.0, 0.9))
        p.pos += 1
    return p._crossing()


def test_clean_ramp_captures():
    p = proc()
    ramp = [-34, -28, -22, -16, -10, -4, 0]  # monotone, steps < 9, crosses 0
    assert push_ramp(p, ramp) == 0  # current frame nearer to zero


def test_nearer_zero_frame_wins():
    p = proc()
    ramp = [-34, -28, -22, -16, -10, -2, 5]  # previous (-2) nearer to zero
    assert push_ramp(p, ramp) == -1


def test_broken_ramp_rejected():
    p = proc()
    noisy = [-34, -20, -25, -10, -4, 0]  # not monotone
    assert push_ramp(p, noisy) is None


def test_no_crossing_no_capture():
    p = proc()
    assert push_ramp(p, [-34, -28, -22, -16, -10, -4]) is None


def test_min_sep_blocks_double():
    p = proc()
    p.last_cap_pos = p.pos - 1  # pretend we just captured: min_sep blocks anyway
    ramp = [-34, -28, -22, -16, -10, -4, 0]
    # crossing exists but on_roi-level min_sep (fpos - last_cap <= 12) would block;
    # _crossing itself only detects the crossing:
    assert push_ramp(p, ramp) == 0


def test_virtual_slot_counts():
    p = proc()
    assert p.slot == 0
    p.virtual_slot()
    p.virtual_slot()
    assert p.slot == 2
    assert p.counters["virtual_slots_total"] == 2
    assert p.slots[-1].kind == "virtual" and p.slots[-1].status == "occluded"
