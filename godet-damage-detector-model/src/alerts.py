"""Streaming port of godet3/flags.py: per-godet state, persistent backlog,
NEW-damage / NEW-splay alerts, tilt watchlist, health.

Method: godet rows accumulate in one table with EXACTLY the batch godets.csv
columns; the four batch rules run verbatim on that table every time a loop
completes. New pending/confirmed alerts (by dedup key kind+godet+loop) are
emitted once via the event sink. Rules come from the bundle (single source).
"""
from __future__ import annotations

from collections import deque

import numpy as np
import pandas as pd


# ---- batch rules, verbatim logic (godet3/flags.py), rules parameterized ----
def persistent_list(G, loops, rules):
    S = G[G["loop"].isin(loops) & (G["n_views"] > 0)]
    P = S.pivot_table(index="godet_id", columns="loop", values="short_any", aggfunc="max")
    seen = P.notna().sum(axis=1)
    hits = P.eq(True).sum(axis=1)
    lip = S.groupby("godet_id")["lip"].median()
    edge = S.assign(e=(S["status_left"] == "edge") | (S["status_right"] == "edge"))
    edge = edge.groupby("godet_id")["e"].mean()
    fill = S.assign(f=S[["fill_L", "fill_R"]].min(axis=1)).groupby("godet_id")["f"].median()
    ids = P.index[(seen >= 2) & (hits >= 2)]
    out = pd.DataFrame({"godet_id": ids, "loops_seen": seen[ids].values,
                        "loops_short": hits[ids].values,
                        "lip_median": lip.reindex(ids).values,
                        "fill_med": fill.reindex(ids).values.round(2),
                        "near_plate": (edge.reindex(ids).values >= 0.5)})
    out["broken"] = out["fill_med"] < rules["broken_fill"]
    out = out.sort_values(["near_plate", "broken", "lip_median"],
                          ascending=[True, False, True])
    return out.reset_index(drop=True)


def new_damage_alerts(G, near_plate_ids, rules):
    G2 = G[G["n_views"] == 2]
    lips = G2.pivot_table(index="godet_id", columns="loop", values="lip", aggfunc="min")
    both = G2.pivot_table(index="godet_id", columns="loop", values="short_both", aggfunc="max")
    loops = sorted(lips.columns)
    rows = []
    for k in loops[1:]:
        prev = [q for q in loops if q < k][-rules["hist_loops"]:]
        for g in lips.index:
            if g in near_plate_ids:
                continue
            h = lips.loc[g, prev].dropna()
            if len(h) < 2 or np.isnan(lips.loc[g, k]):
                continue
            base = float(h.median())
            if lips.loc[g, k] <= base - rules["drop_rows"] and both.loc[g, k] is True:
                nxt = k + 1 if (k + 1) in loops else None
                if nxt is None or np.isnan(lips.loc[g, nxt]):
                    st = "pending"
                elif lips.loc[g, nxt] <= base - rules["drop_rows"]:
                    st = "confirmed"
                else:
                    st = "not confirmed"
                rows.append({"godet_id": g, "loop": k, "lip_before": base,
                             "lip_now": lips.loc[g, k], "drop": base - lips.loc[g, k],
                             "status": st})
    return pd.DataFrame(rows, columns=["godet_id", "loop", "lip_before", "lip_now",
                                       "drop", "status"]), lips


def splay_alerts(G, near_plate_ids, rules):
    if "straight_L" not in G.columns or "straight_R" not in G.columns:
        return pd.DataFrame(columns=["godet_id", "loop", "s_before", "s_now", "rise", "status"])
    G2 = G[G["n_views"] == 2]
    sL = G2.pivot_table(index="godet_id", columns="loop", values="straight_L", aggfunc="min")
    sR = G2.pivot_table(index="godet_id", columns="loop", values="straight_R", aggfunc="min")
    loops = sorted(sL.columns)
    rows = []
    for k in loops[1:]:
        prev = [q for q in loops if q < k][-rules["hist_loops"]:]
        for g in sL.index:
            if g in near_plate_ids:
                continue
            hL = sL.loc[g, prev].dropna()
            hR = sR.loc[g, prev].dropna()
            if len(hL) < 2 or len(hR) < 2 or np.isnan(sL.loc[g, k]) or np.isnan(sR.loc[g, k]):
                continue
            bL, bR = float(hL.median()), float(hR.median())
            if sL.loc[g, k] >= bL + rules["splay_rise"] and sR.loc[g, k] >= bR + rules["splay_rise"]:
                rise = min(float(sL.loc[g, k]) - bL, float(sR.loc[g, k]) - bR)
                nxt = k + 1 if (k + 1) in loops else None
                if nxt is None or np.isnan(sL.loc[g, nxt]) or np.isnan(sR.loc[g, nxt]):
                    st = "pending"
                elif sL.loc[g, nxt] >= bL + rules["splay_rise"] and sR.loc[g, nxt] >= bR + rules["splay_rise"]:
                    st = "confirmed"
                else:
                    st = "not confirmed"
                rows.append({"godet_id": g, "loop": k,
                             "s_before": round((bL + bR) / 2, 2),
                             "s_now": round((float(sL.loc[g, k]) + float(sR.loc[g, k])) / 2, 2),
                             "rise": round(rise, 2), "status": st})
    return pd.DataFrame(rows, columns=["godet_id", "loop", "s_before", "s_now", "rise", "status"])


def nonparallel_list(G, tmpl_lines, near_plate_ids, rules):
    if "slope_L" not in G.columns or "slope_R" not in G.columns:
        return pd.DataFrame(columns=["godet_id", "loops", "slopeDevL", "slopeDevR", "lip_median"])
    G2 = G[(G["n_views"] == 2) & (~G["godet_id"].isin(near_plate_ids))]
    rows = []
    for g, S in G2.groupby("godet_id"):
        if len(S) < 3:
            continue
        dL = (S["slope_L"] - tmpl_lines["L"][0]).median(skipna=True)
        dR = (S["slope_R"] - tmpl_lines["R"][0]).median(skipna=True)
        if pd.isna(dL) or pd.isna(dR):
            continue
        if abs(dL) >= rules["slope_dev_l"] and abs(dR) >= rules["slope_dev_r"]:
            rows.append({"godet_id": int(g), "loops": len(S),
                         "slopeDevL": round(float(dL), 4), "slopeDevR": round(float(dR), 4),
                         "lip_median": round(float(S["lip"].median()), 0)})
    out = pd.DataFrame(rows, columns=["godet_id", "loops", "slopeDevL", "slopeDevR", "lip_median"])
    if len(out):
        out["worst"] = out[["slopeDevL", "slopeDevR"]].abs().min(axis=1)
        out = out.sort_values("worst", ascending=False).drop(columns="worst").reset_index(drop=True)
    return out


# ---- streaming tracker ------------------------------------------------------
class AlertTracker:
    """Accumulates godet rows; recomputes lists on loop completion; emits new
    pending/confirmed events once (dedup key kind+godet+loop)."""

    def __init__(self, rules, tmpl_lines):
        self.rules = rules
        self.tmpl_lines = tmpl_lines
        self.rows = []
        self.loop_starts = {}      # loop_no -> start slot (span = next start - start)
        self.computed = set()      # loops fully recomputed
        self.counts = {}           # loop_no -> rows received
        self.persistent = pd.DataFrame()
        self.alerts = pd.DataFrame()
        self.splay = pd.DataFrame()
        self.nonpar = pd.DataFrame()
        self.health_lines = []
        self.seen_alert_keys = set()
        self.events = []           # emitted (kind, godet_id, loop, state, payload)
        self.sinks = []
        self.complete_sinks = []   # on_loop_complete(loop_no) callbacks (snapshots)
        self.peaks = deque(maxlen=int(rules["template_lost_window"]))
        self.cap_times = deque(maxlen=4096)

    def subscribe(self, fn):
        self.sinks.append(fn)

    def on_complete(self, fn):
        self.complete_sinks.append(fn)

    def on_loop_start(self, loop_no, start_slot):
        self.loop_starts[loop_no] = start_slot

    def on_godet_row(self, row):
        self.rows.append(row)
        k = row["loop"]
        self.counts[k] = self.counts.get(k, 0) + 1
        if row["n_views"] > 0:
            self.peaks.append(row["peak_left"])
            self.cap_times.append(row["t_left"])
        # loop k complete when loop k+1 started AND all its rows arrived
        for c in list(self.counts):
            if c in self.computed or c + 1 not in self.loop_starts:
                continue
            if self.counts[c] >= self.loop_starts[c + 1] - self.loop_starts[c]:
                self._recompute(c)

    def _table(self):
        return pd.DataFrame(self.rows)

    def _recompute(self, loop_done):
        G = self._table()
        loops = sorted(G["loop"].unique())
        last3 = [l for l in loops if l <= loop_done][-self.rules["hist_loops"]:]
        self.persistent = persistent_list(G, last3, self.rules)
        near_plate = set(self.persistent[self.persistent["near_plate"]]["godet_id"])
        A, lips = new_damage_alerts(G, near_plate, self.rules)
        self.alerts = A
        self.splay = splay_alerts(G, near_plate, self.rules)
        self.nonpar = nonparallel_list(G, self.tmpl_lines, near_plate, self.rules)
        self.health_lines = self._health(lips)
        for _, r in A[A["status"].isin(("pending", "confirmed"))].iterrows():
            self._emit_once("damage", r)
        for _, r in self.splay[self.splay["status"].isin(("pending", "confirmed"))].iterrows():
            self._emit_once("splay", r)
        self.computed.add(loop_done)
        for fn in self.complete_sinks:
            fn(loop_done)

    def _emit_once(self, kind, r):
        key = (kind, int(r["godet_id"]), int(r["loop"]))
        if key in self.seen_alert_keys:
            return
        self.seen_alert_keys.add(key)
        ev = (kind, int(r["godet_id"]), int(r["loop"]), r["status"], dict(r))
        self.events.append(ev)
        for fn in self.sinks:
            fn(ev)

    def _health(self, lips):
        lines = []
        pk = pd.Series(list(self.peaks))
        if len(pk) >= 50 and float(pk.rolling(200, min_periods=50).median().iloc[-1]) < self.rules["template_lost_peak"]:
            lines.append("TEMPLATE LOST (camera moved / ROI changed?): rolling median peak < %.2f" % float(self.rules["template_lost_peak"]))
        if self.cap_times:
            tmax = max(self.cap_times)
            recent = sum(1 for t in self.cap_times if tmax - t < 60)
            if tmax - min(self.cap_times) >= 120 and recent < self.rules["trigger_lost_per_min"]:
                lines.append(f"TRIGGER LOST / CONVEYOR STOPPED: {recent} captures in the last 60 s")
        if isinstance(lips, pd.DataFrame) and len(lips.columns):
            loops = sorted(lips.columns)
            for k in loops[1:]:
                prev = [q for q in loops if q < k][-self.rules["hist_loops"]:]
                base = lips[prev].median(axis=1)
                chg = (lips[k] <= base - self.rules["drop_rows"]) & base.notna() & lips[k].notna()
                n_cov = int((base.notna() & lips[k].notna()).sum())
                if n_cov and chg.sum() / n_cov > self.rules["population_alarm_frac"]:
                    lines.append(f"POPULATION CHANGE in loop {k}: {int(chg.sum())}/{n_cov} godets "
                                 f"dropped >= {self.rules['drop_rows']} rows -> camera / light / "
                                 "calibration, not damage")
        return lines

    def flush(self):
        """End-of-stream only: recompute every incomplete loop, then the final
        persistent/alert/watchlist state exactly like batch flags.py main()."""
        G = self._table()
        if not len(G):
            return
        loops = sorted(G["loop"].unique())
        for c in loops:
            if c not in self.computed and self.counts.get(c, 0) > 0:
                self._recompute(c)
        last3 = loops[-self.rules["hist_loops"]:]
        self.persistent = persistent_list(G, last3, self.rules)
        near_plate = set(self.persistent[self.persistent["near_plate"]]["godet_id"])
        A, lips = new_damage_alerts(G, near_plate, self.rules)
        self.alerts = A
        self.splay = splay_alerts(G, near_plate, self.rules)
        self.nonpar = nonparallel_list(G, self.tmpl_lines, near_plate, self.rules)
        self.health_lines = self._health(lips)
        for _, r in A[A["status"].isin(("pending", "confirmed"))].iterrows():
            self._emit_once("damage", r)
        for _, r in self.splay[self.splay["status"].isin(("pending", "confirmed"))].iterrows():
            self._emit_once("splay", r)

    # -- state for store -------------------------------------------------------
    def snapshot(self):
        return {"rows": self.rows, "loop_starts": self.loop_starts,
                "computed": sorted(self.computed),
                "seen_alert_keys": sorted(self.seen_alert_keys)}

    def restore(self, snap):
        self.rows = list(snap["rows"])
        self.loop_starts = {int(k): v for k, v in snap["loop_starts"].items()}
        self.computed = set(snap["computed"])
        self.seen_alert_keys = set(tuple(k) for k in snap["seen_alert_keys"])
        self.counts = {}
        for r in self.rows:
            self.counts[r["loop"]] = self.counts.get(r["loop"], 0) + 1
        if self.rows:
            G = self._table()
            loops = sorted(G["loop"].unique())
            last3 = loops[-self.rules["hist_loops"]:]
            self.persistent = persistent_list(G, last3, self.rules)
            near_plate = set(self.persistent[self.persistent["near_plate"]]["godet_id"])
            A, lips = new_damage_alerts(G, near_plate, self.rules)
            self.alerts = A
            self.splay = splay_alerts(G, near_plate, self.rules)
            self.nonpar = nonparallel_list(G, self.tmpl_lines, near_plate, self.rules)
            self.health_lines = self._health(lips)
