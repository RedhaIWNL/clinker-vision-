"""Restart recovery (fast, no video): kill exactly at a snapshot tip, restore
into fresh instances, continue — final Tier-2 state must equal an
uninterrupted run (rows, persistent, alerts, no duplicate events).

Crash-consistency is at snapshot granularity by design (periodic snapshots
every 200 slots + every completed loop; a kill between snapshots loses at most
the trailing partial window — the same class as recording boundaries). The
duplicate-slot guard makes sender redelivery harmless.

Run:  python -m pytest tests/test_restart.py -q -s
"""
import sys
from pathlib import Path

import pandas as pd

from support import CAM, ROOT, slots_from_captures

sys.path.insert(0, str(ROOT))

from src.alerts import AlertTracker  # noqa: E402
from src.bundle import load_bundle  # noqa: E402
from src.identity import StreamingIdentity  # noqa: E402
from src.store import Store  # noqa: E402

H00 = CAM / "out" / "h00"


def make_stack(bundle, store_path=None):
    ident = StreamingIdentity(bundle.master, bundle.loop_slots, bundle.rules)
    alerts = AlertTracker(bundle.rules, bundle.lines)
    ident.subscribe(alerts.on_godet_row)
    ident.subscribe_loops(alerts.on_loop_start)
    store = Store(store_path) if store_path else None
    if store is not None:
        alerts.on_complete(
            lambda loop: store.save(bundle.version, ident.snapshot(),
                                    alerts.snapshot()["rows"],
                                    alerts.snapshot()["computed"],
                                    alerts.snapshot()["seen_alert_keys"],
                                    alerts.snapshot()["loop_starts"]))
    return ident, alerts, store


def feed(stack, recs):
    ident, alerts, _ = stack
    for rec in recs:
        ident.on_slot(rec)
    ident.flush()
    alerts.flush()


def state_of(stack):
    ident, alerts, _ = stack
    G = pd.DataFrame(alerts.rows).sort_values(["loop", "slot"]).reset_index(drop=True)
    P = sorted(alerts.persistent["godet_id"]) if len(alerts.persistent) else []
    A = sorted(zip(alerts.alerts["godet_id"], alerts.alerts["loop"],
                   alerts.alerts["status"])) if len(alerts.alerts) else []
    return ident.starts, G, P, A, sorted(alerts.events)


def test_restart_recovers(tmp_path):
    bundle = load_bundle(ROOT / "model")

    ref = make_stack(bundle)
    feed(ref, slots_from_captures(H00 / "captures.csv"))

    # run A until the second snapshot lands, then kill (no flush): the snapshot
    # tip is the exact recovery point.
    a = make_stack(bundle, str(tmp_path / "store.db"))
    saves = []
    orig_save = a[2].save
    a[2].save = lambda *args: (saves.append(args[1].get("max_slot")), orig_save(*args))
    recs = slots_from_captures(H00 / "captures.csv")
    kill_at = None
    for rec in recs:
        a[0].on_slot(rec)
        if len(saves) >= 2:
            kill_at = rec.slot
            break
    assert kill_at is not None, "no snapshot fired"
    tip = saves[-1]
    print(f"\nkilled at slot {kill_at}, snapshot tip {tip}")

    b = make_stack(bundle, str(tmp_path / "store.db"))
    restored = b[2].load(bundle.version)
    assert restored is not None, "no snapshot to restore"
    b[0].restore(restored["identity"])
    b[0].restore_tail(restored["tail"])
    b[1].restore({"rows": restored["rows"], "loop_starts": restored["loop_starts"],
                  "computed": restored["computed"],
                  "seen_alert_keys": restored["alert_keys"]})
    for k, st in enumerate(restored["identity"].get("starts", [])):
        b[1].on_loop_start(k, st)
    # continue with FRESH objects past the tip (as production would); a small
    # overlap re-feeds already-seen slots and must register as duplicates.
    fresh = slots_from_captures(H00 / "captures.csv")
    by_slot = {r.slot: r for r in fresh}
    for s in range(tip - 50, max(by_slot) + 1):
        if s in by_slot:
            b[0].on_slot(by_slot[s])
    b[0].flush()
    b[1].flush()
    assert b[0].duplicates >= 50, "overlap redelivery was not detected as duplicates"

    s_ref, s_got = state_of(ref), state_of(b)
    assert s_got[0] == s_ref[0], "starts differ after restart"
    pd.testing.assert_frame_equal(s_got[1].drop(columns=["t_left", "t_right"]),
                                  s_ref[1].drop(columns=["t_left", "t_right"]),
                                  check_dtype=False)
    assert s_got[2] == s_ref[2] and s_got[3] == s_ref[3], "lists differ after restart"
    assert s_got[4] == s_ref[4], "events differ after restart"
    print("restart at snapshot tip: final state identical, no duplicate events")
