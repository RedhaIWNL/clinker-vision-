"""Phase 3 gate (fast, no video): stream h00 slot records through
StreamingIdentity + AlertTracker and reproduce the batch Tier-2 outputs
(godets.csv, persistent.csv, alerts.csv, alerts_splay.csv) exactly.

t_* columns differ by construction (wall-clock vs video seconds) and are
compared as orderings only; every other column must match (ints exact,
floats ±1e-6).

Run:  python -m pytest tests/test_tier2_batch.py -q -s
"""
import sys
from pathlib import Path

import numpy as np
import pandas as pd

from support import CAM, ROOT, slots_from_captures

sys.path.insert(0, str(ROOT))

from src.alerts import AlertTracker  # noqa: E402
from src.bundle import load_bundle  # noqa: E402
from src.identity import StreamingIdentity  # noqa: E402

H00 = CAM / "out" / "h00"
NUMERIC_TOL = 1e-6


def run_stream(recs, bundle):
    ident = StreamingIdentity(bundle.master, bundle.loop_slots, bundle.rules)
    alerts = AlertTracker(bundle.rules, bundle.lines)
    ident.subscribe(alerts.on_godet_row)
    ident.subscribe_loops(alerts.on_loop_start)
    for rec in recs:
        ident.on_slot(rec)
    ident.flush()
    alerts.flush()
    return ident, alerts


def test_tier2_reproduces_batch():
    bundle = load_bundle(ROOT / "model")
    recs = slots_from_captures(H00 / "captures.csv")
    ident, alerts = run_stream(recs, bundle)

    assert ident.locked, "identity never locked on h00"
    assert ident.starts == [0, 1208, 2416, 3624], f"starts {ident.starts}"
    print(f"\nstarts {ident.starts} scores "
          f"{[round(s, 2) for s in ident.scores]} rows {len(alerts.rows)}")

    G = pd.DataFrame(alerts.rows).sort_values(["loop", "slot"]).reset_index(drop=True)
    ref = pd.read_csv(H00 / "godets.csv").sort_values(["loop", "slot"]).reset_index(drop=True)
    assert len(G) == len(ref), f"{len(G)} rows vs batch {len(ref)}"
    for c in ("godet_id", "loop", "slot", "n_views", "short_L", "short_R",
              "short_both", "short_any", "status_left", "status_right"):
        assert (G[c].fillna("∅").values == ref[c].fillna("∅").values).all(), f"col {c}"
    for c in ("lit_L", "lit_R", "lip", "straight_L", "straight_R", "face_max",
              "slope_L", "slope_R", "fill_L", "fill_R", "face_resid", "peak_left"):
        a, b = G[c].values.astype(float), ref[c].values.astype(float)
        m = ~(np.isnan(a) | np.isnan(b))
        assert (np.isnan(a) == np.isnan(b)).all(), f"NaN pattern col {c}"
        assert np.abs(a[m] - b[m]).max() < NUMERIC_TOL, f"col {c}"
    # t_* are wall-clock here vs video seconds in batch: orderings must agree.
    assert (G["t_left"].values[:-1] <= G["t_left"].values[1:]).all()

    P = alerts.persistent
    Pref = pd.read_csv(H00 / "persistent.csv")
    assert set(P["godet_id"]) == set(Pref["godet_id"]), "persistent id sets differ"
    m = P.set_index("godet_id")
    mref = Pref.set_index("godet_id")
    for c in ("loops_seen", "loops_short", "near_plate"):
        assert (m[c] == mref[c]).all(), f"persistent col {c}"
    assert np.abs(m["lip_median"] - mref["lip_median"]).max() < NUMERIC_TOL

    for name in ("alerts", "splay"):
        A = getattr(alerts, name)
        fname = "alerts_splay.csv" if name == "splay" else "alerts.csv"
        Aref = pd.read_csv(H00 / fname)
        assert len(A) == len(Aref), f"{name}: {len(A)} vs batch {len(Aref)}"
        if len(A):
            k = ["godet_id", "loop", "status"]
            a = A.sort_values(k).reset_index(drop=True)
            b = Aref.sort_values(k).reset_index(drop=True)
            assert (a[k] == b[k]).all(axis=None), f"{name} keys/status differ"
    print(f"persistent {len(P)} alerts {len(alerts.alerts)} "
          f"splay {len(alerts.splay)} nonpar {len(alerts.nonpar)} "
          f"health: {alerts.health_lines or ['all clear']}")
    assert not any(l.startswith("POPULATION") for l in alerts.health_lines)
