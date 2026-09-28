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
    """Locked; map position = column + offset. Anchors trail the newest column by 2000 (a piece)."""
    def __init__(self, offset=0.0, bad=None):
        self.locked = True
        self.offset = offset
        self.bad = bad or []                            # [(col_from, col_to, error)]: wrong anchors there
        self.anchors = deque([(0, offset)], maxlen=16)
        self.loop_base, self.loop_offset = 0, 0
        self.counters = {"locks_total": 1}

    def advance(self, newest_col):
        c = newest_col - 2000
        if c > self.anchors[-1][0]:
            err = sum(e for a, z, e in self.bad if a <= c <= z)
            self.anchors.append((c, c + self.offset + err))


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


def feed(t, b, loops, first_loop=0, drop=None, unreadable=None, shift=None):
    L, P = b.ident["loop_cols"], b.ident["pitch"]
    for lp in range(first_loop, first_loop + loops):
        hidden = np.zeros(L, bool)
        for g in wheel_godets(b.ident["godets"]):
            if drop and g == drop[0] and lp >= drop[1]:
                continue
            c = int((g + 0.5) * P)
            if shift and lp == shift[0] and shift[1] <= g <= shift[2]:
                c += shift[3]                          # this pass placed a stretch a little off
            hidden[c - 60:c + 60] = True
        readable = np.ones(L, bool)
        if unreadable:
            readable[int(unreadable[0] * P):int((unreadable[1] + 1) * P)] = False
        cols = np.arange(lp * L, (lp + 1) * L)
        for a in range(0, L, 2000):
            n = len(cols[a:a + 2000])
            t.ident.advance(int(cols[a:a + 2000][-1]))
            t.on_wheels(cols[a:a + 2000], hidden[a:a + 2000], readable[a:a + 2000], np.zeros((200, n), np.uint8))
    end = (first_loop + loops) * L                    # some columns of the next loop close the pass
    t.ident.advance(end + 4000)
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
    feed(t, b, loops=3)
    k1 = {k for v in flags(t).values() for *_, k in v}
    feed(t, b, loops=2, first_loop=3)                                # more passes, same lock
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


def test_keys_survive_a_relock_with_shifted_placement():
    """After a re-lock (restart, noon) placements differ locally by up to ~1 godet: the same places
    must keep their keys, and no extra flag may be created."""
    b, t = make()
    feed(t, b, loops=3)
    k1 = {k for v in flags(t).values() for *_, k in v}
    L = b.ident["loop_cols"]
    t.ident = FakeIdentity(offset=-1.5 * b.ident["pitch"])          # new lock, placed 1.5 godets off
    t.ident.anchors.append((3 * L, 3 * L + t.ident.offset))
    t.ident.counters["locks_total"] = 2
    feed(t, b, loops=3, first_loop=3)
    k2 = {k for v in flags(t).values() for *_, k in v}
    assert k1 and k1 == k2, (k1, k2)
    assert set(t.flags) == k1, sorted(t.flags)                       # nothing created along the way


def test_a_locally_shifted_pass_creates_no_false_gap():
    """2026-09-07 replay: one pass placed ~400 godets 50-100 columns off; wheels must still match."""
    b, t = make()
    feed(t, b, loops=4, shift=(1, 400, 800, 90))
    f = flags(t)
    assert [x[:2] for x in f["wheel_gap"]] == [(101, 107)], f["wheel_gap"]
    assert set(t.flags) == {k for v in f.values() for *_, k in v}, sorted(t.flags)


def test_a_wrong_anchor_creates_no_false_gap():
    """After the noon re-lock (2026-09-07 replay) one wrong anchor squeezed ~16 godets of chain onto 2
    and opened a 15-godet "gap". A stretch that does not map ~1:1 must count as not seen."""
    b, t = make()
    L, P = b.ident["loop_cols"], b.ident["pitch"]
    t.ident = FakeIdentity(bad=[(lp * L + 600 * P, lp * L + 620 * P, -1500.0) for lp in (1, 2)])
    feed(t, b, loops=3)                                              # wrong anchors on 2 of 3 passes
    f = flags(t)
    assert [x[:2] for x in f["wheel_gap"]] == [(101, 107)], f["wheel_gap"]
    assert t.counters["wheels_unplaced_total"] > 0


def test_no_flag_is_raised_before_three_passes():
    """1 or 2 passes are not enough to judge the wheel pattern (one missed wheel = a false gap)."""
    b, t = make()
    feed(t, b, loops=2)
    assert flags(t) == {} and t.counters["wheel_evaluations_total"] == 0
    feed(t, b, loops=1, first_loop=2)
    assert [x[:2] for x in flags(t)["wheel_gap"]] == [(101, 107)]
