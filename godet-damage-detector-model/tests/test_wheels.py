"""Wheel (galet) tracker rules on a synthetic chain (fast, no video).

The chain has a wheel every 4 godets, except a planted gap (rule A), a planted pair of close
wheels (rule B), and one wheel that falls off after two loops (rule M). The stream side is
simulated: hidden-stripe runs at the wheel columns, everything readable, identity locked with
map position = column.

Run:  python -m pytest tests/test_wheels.py -q
"""
import sys
from collections import deque
from pathlib import Path

import numpy as np

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src" / "gen"))
sys.path.insert(0, str(ROOT))

import inference_v2_pb2 as pb2  # noqa: E402
from src.cam4.bundle import load_cam4_bundle  # noqa: E402
from src.cam4.wheels import WheelTracker  # noqa: E402

BUNDLE = ROOT / "model" / "cam4"


class FakeIdentity:
    """Locked; map position = column."""
    def __init__(self):
        self.locked = True
        self.anchors = deque([(0, 0.0)])
        self.loop_base, self.loop_offset = 0, 0
        self.counters = {"locks_total": 1}


def wheel_godets(n_godets):
    """A wheel every 4 godets; none from 101 to 107 (7 empty godets: rule A); an extra wheel
    at 301 next to the one at 300 (2 wheels in 2 godets: rule B)."""
    # 0, 4, ..., 1200, then 1205: every gap is 4 godets, two are 5, so the loop closes cleanly
    g = [x for x in list(range(0, 1201, 4)) + [1205] if not 101 <= x <= 107]
    return sorted(g + [301])


def make():
    b = load_cam4_bundle(BUNDLE)
    b.ident = dict(b.ident, phase=0.0)               # simpler arithmetic in the test
    return b, WheelTracker(b, FakeIdentity(), "CAM-4")


def feed(t, b, loops, first_loop=0, drop=None, unreadable=None):
    L, P = b.ident["loop_cols"], b.ident["pitch"]
    for lp in range(first_loop, first_loop + loops):
        hidden = np.zeros(L, bool)
        for g in wheel_godets(b.ident["godets"]):
            if drop and g == drop[0] and lp >= drop[1]:
                continue
            c = int((g + 0.5) * P)
            hidden[c - 60:c + 60] = True
        readable = np.ones(L, bool)
        if unreadable:
            readable[int(unreadable[0] * P):int((unreadable[1] + 1) * P)] = False
        cols = np.arange(lp * L, (lp + 1) * L)
        for a in range(0, L, 2000):
            n = len(cols[a:a + 2000])
            t.on_wheels(cols[a:a + 2000], hidden[a:a + 2000], readable[a:a + 2000], np.zeros((200, n), np.uint8))
    end = (first_loop + loops) * L                    # a few columns of the next loop close the pass
    t.on_wheels(np.arange(end, end + 10), np.zeros(10, bool), np.ones(10, bool), np.zeros((200, 10), np.uint8))


def flags(t):
    r = pb2.GodetStateResponse()
    t.events(r)
    out = {}
    for e in r.events:
        out.setdefault(e.kind, []).append((int(e.measurements["first_godet"]), int(e.measurements["last_godet"]),
                                           e.event_key))
    return out


def test_gap_and_density_rules_fire_where_planted():
    b, t = make()
    feed(t, b, loops=3)
    f = flags(t)
    assert f.get("wheel_gap") and [x[:2] for x in f["wheel_gap"]] == [(101, 107)], f.get("wheel_gap")
    assert f["wheel_gap"][0][2] == "CAM-4:WHEEL_GAP:101"            # stable key, no loop number
    dens = f.get("wheel_density", [])
    assert [d[:2] for d in dens] == [(299, 302)], dens              # windows 299-301 and 300-302 merged
    assert "wheel_missing" not in f
    assert t.counters["wheels_total"] >= 3 * 295


def test_keys_are_stable_over_loops():
    b, t = make()
    feed(t, b, loops=2)
    k1 = {k for v in flags(t).values() for *_, k in v}
    feed(t, b, loops=2, first_loop=2)                                # more passes, same lock
    k2 = {k for v in flags(t).values() for *_, k in v}
    assert k1 and k1 == k2


def test_a_wheel_that_falls_off_is_reported_missing():
    b, t = make()
    feed(t, b, loops=5, drop=(600, 3))                               # godet 600's wheel gone from loop 3
    f = flags(t)
    miss = f.get("wheel_missing", [])
    assert [m[0] for m in miss] == [600], miss
    assert miss[0][2].startswith("CAM-4:WHEEL_MISSING:600:")


def test_unreadable_stretches_never_create_gaps():
    b, t = make()
    feed(t, b, loops=3, unreadable=(800, 811))                       # dust over godets 800..811
    gaps = flags(t).get("wheel_gap", [])
    assert all(not (a <= 805 <= z) for a, z, _ in gaps), gaps       # unseen is not "empty"
    assert [x[:2] for x in gaps] == [(101, 107)]


def test_state_is_json_serialisable():
    import json
    b, t = make()
    feed(t, b, loops=3)
    json.dumps(t.snapshot())                                         # the engine saves it as JSON
