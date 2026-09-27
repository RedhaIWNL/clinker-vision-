"""CAM-4 godet identity (chain map tracking) and per-godet history.

Identity: the bundle's chain map is the fingerprint (edge profile + texture) of one loop.
Pieces of the incoming chain are matched against it. Unlocked: search the whole map and
lock after two pieces agree. Locked: follow the chain (the odometer predicts where the next
piece should land) and confirm each piece in a small window, which tolerates the lighting
changes that make whole-map search fail (batch: noon only matched 25 % of pieces globally).
A godet is one pitch of the map; a pass is one godet crossing the slit.

History: every pass gives the godet's peak severity. Light changes the score level, so each
pass is scored relative to the recent population of passes (one loop):
relative = (raw - median) / (p90 - median). A godet is `confirmed` when it has >= 2 passes
and the median of its relative scores is >= godet_alarm; `suspect` when only its latest pass
is strong. Deviation from batch: batch normalised by the whole pass; streaming by the
trailing population (the same majority, one loop behind).
"""
from __future__ import annotations

from collections import deque
from dataclasses import asdict, dataclass

import cv2
import numpy as np

LOCAL_WINDOW = 3000         # locked search: +- columns around the prediction
LOCAL_MIN = 0.30            # locked acceptance (a small window rarely matches by chance)
LOCK_AGREE = 300            # two unlocked pieces must agree this closely to lock
LOST_COLS = 60000           # locked but no confirmed piece for this long -> resync
MIN_POPULATION = 300        # passes needed before relative scores mean anything
STD_ALPHA = 1.0 / 20000     # running per-channel scale of the fingerprint


@dataclass
class PassRecord:
    godet_id: int
    loop: int
    raw: float
    best_col: int
    top_row: float          # edge row (local to mosaic row 40) at the strongest column
    columns: int
    sequence_no: object = None


class Cam4Identity:
    def __init__(self, bundle):
        idn = bundle.ident
        self.ref = bundle.chainmap
        self.L = int(idn["loop_cols"]); self.pitch = float(idn["pitch"])
        self.phase = float(idn["phase"]); self.n = int(idn["godets"])
        self.piece, self.step = int(idn["piece"]), int(idn["step"])
        self.min_match = float(idn["min_match"])
        self.ext = np.concatenate([self.ref, self.ref[:, :self.piece + 10]], 1)
        self.var = np.square(np.array(idn["fingerprint_std"], np.float32))
        self.fp = np.zeros((2, 0), np.float32); self.fp0 = 0      # normalised fingerprint buffer
        self.next_piece_end = None
        self.pending = deque()        # (col, sev, top, missing) waiting for a position
        self.locked = False
        self.resyncing = False
        self.cand = None              # (centre col, map pos) of an unlocked candidate
        self.anchors = deque(maxlen=16)   # (col, unwrapped map pos)
        self.loop_base = None         # unwrapped-loop number of loop 0
        self.loop_offset = 0          # continues loop numbering across re-locks and restarts
        self.last_loop = -1
        self.cur = None               # current pass accumulator
        self.sinks = []
        self.counters = {"pieces_total": 0, "anchors_total": 0, "locks_total": 0,
                         "lock_losses_total": 0, "passes_total": 0}

    def subscribe(self, fn):
        self.sinks.append(fn)

    # ---- input from the stream -------------------------------------------------------
    def on_columns(self, cols, fp_raw, sev, top, missing):
        if len(cols) == 0:
            return
        ok = ~missing
        if ok.any():
            self.var = (1 - STD_ALPHA * ok.sum()) * self.var + STD_ALPHA * ok.sum() * np.mean(
                np.square(fp_raw[:, ok]), 1)
        self.var = self.var.astype(np.float32)
        fpn = (fp_raw / np.sqrt(np.maximum(self.var, 1e-6))[:, None]).astype(np.float32)
        fpn[:, missing] = 0
        if self.fp.shape[1] == 0:
            self.fp0 = int(cols[0])
            self.next_piece_end = self.fp0 + self.piece
        self.fp = np.concatenate([self.fp, fpn], 1)
        for c, s, t, m in zip(cols, sev, top, missing):
            self.pending.append((int(c), float(s), float(t), bool(m)))
        end = self.fp0 + self.fp.shape[1]
        while self.next_piece_end <= end:
            a = self.next_piece_end - self.piece
            self._piece(np.ascontiguousarray(self.fp[:, a - self.fp0:self.next_piece_end - self.fp0]), a + self.piece // 2)
            self.next_piece_end += self.step
        keep_from = self.next_piece_end - self.piece
        if keep_from > self.fp0:
            self.fp = self.fp[:, keep_from - self.fp0:]; self.fp0 = keep_from

    # ---- matching ----------------------------------------------------------------------
    def _circ(self, d):
        return (d + self.L / 2) % self.L - self.L / 2

    def _predict(self, col):
        """Unwrapped map position of a column from the anchors (odometer scale fitted)."""
        c_last, p_last = self.anchors[-1]
        scale = 1.0
        if len(self.anchors) >= 3:
            cs = np.array([a[0] for a in self.anchors], float); ps = np.array([a[1] for a in self.anchors], float)
            if cs[-1] - cs[0] >= 15000:
                scale = float(np.clip(np.polyfit(cs, ps, 1)[0], 0.98, 1.02))
        return p_last + (col - c_last) * scale

    def _piece(self, piece, centre):
        self.counters["pieces_total"] += 1
        if not np.any(piece):
            return
        if not self.locked:
            r = cv2.matchTemplate(self.ext, piece, cv2.TM_CCOEFF_NORMED)[0]
            k = int(np.argmax(r))
            if r[k] < self.min_match:
                self.cand = None
                return
            pos = (k + self.piece // 2) % self.L
            if self.cand is not None and abs(self._circ((pos - self.cand[1]) - (centre - self.cand[0]))) < LOCK_AGREE:
                p0 = self.cand[1]
                self.anchors.clear()
                self.anchors.append((self.cand[0], float(p0)))
                self.anchors.append((centre, float(p0 + self._circ(pos - p0 - (centre - self.cand[0])) + (centre - self.cand[0]))))
                self.locked, self.resyncing, self.cand = True, False, None
                self.counters["locks_total"] += 1
                if self.loop_base is not None:              # re-lock: keep counting loops forward
                    self.loop_offset = self.last_loop + 1
                self.loop_base = int(np.floor((self.anchors[0][1] - self.phase) / self.L))
                self._map_pending(centre)
            else:
                self.cand = (centre, pos)
            return
        pred = self._predict(centre)
        lo = int(np.floor(pred)) - LOCAL_WINDOW - self.piece // 2
        idx = np.arange(lo, lo + 2 * LOCAL_WINDOW + self.piece) % self.L
        seg = self.ref[:, idx]
        r = cv2.matchTemplate(seg, piece, cv2.TM_CCOEFF_NORMED)[0]
        k = int(np.argmax(r))
        if r[k] >= LOCAL_MIN:
            pos = lo + k + self.piece // 2                     # unwrapped, near pred
            self.anchors.append((centre, float(pos)))
            self.counters["anchors_total"] += 1
        elif centre - self.anchors[-1][0] > LOST_COLS:
            self.locked, self.resyncing = False, True
            self.counters["lock_losses_total"] += 1
            self._close_pass()
            return
        self._map_pending(centre)

    # ---- columns -> godets -> passes ---------------------------------------------------
    def _map_pending(self, upto):
        while self.pending and self.pending[0][0] <= upto:
            c, s, t, m = self.pending.popleft()
            pos = self._predict(c)
            q = pos - self.phase
            loop = max(int(np.floor(q / self.L)) - self.loop_base, 0) + self.loop_offset
            self.last_loop = max(self.last_loop, loop)
            gid = int((q % self.L) // self.pitch) % self.n
            key = (loop, gid)
            if self.cur is None or self.cur["key"] != key:
                self._close_pass()
                self.cur = {"key": key, "raw": -np.inf, "best": c, "top": t, "n": 0, "miss": 0}
            cu = self.cur
            cu["n"] += 1; cu["miss"] += int(m)
            if not m and s > cu["raw"]:
                cu["raw"], cu["best"], cu["top"] = s, c, t
        if not self.locked:
            self.pending.clear()

    def _close_pass(self):
        cu, self.cur = self.cur, None
        if cu is None or cu["n"] < 0.8 * self.pitch or cu["miss"] > 0.2 * cu["n"] or not np.isfinite(cu["raw"]):
            return
        rec = PassRecord(godet_id=cu["key"][1], loop=cu["key"][0], raw=float(cu["raw"]),
                         best_col=int(cu["best"]), top_row=float(cu["top"]), columns=int(cu["n"]))
        self.counters["passes_total"] += 1
        for fn in self.sinks:
            fn(rec)

    # ---- persistence -------------------------------------------------------------------
    def snapshot(self):
        return {"counters": dict(self.counters), "loop_offset": int(max(self.last_loop, self.loop_offset - 1)) + 1,
                "var": self.var.tolist()}

    def restore(self, snap):
        self.counters.update(snap.get("counters", {}))
        self.loop_offset = int(snap.get("loop_offset", 0))
        if "var" in snap:
            self.var = np.array(snap["var"], np.float32)


class Cam4History:
    def __init__(self, rules, n_godets):
        self.alarm = float(rules["godet_alarm_relative"])
        self.pass_alarm = float(rules["pass_alarm_relative"])
        self.min_passes = int(rules["min_passes"])
        self.keep = int(rules["history_passes"])
        self.n = n_godets
        self.pop = deque(maxlen=int(rules["population_passes"]))
        self.early = []                   # passes seen before the population was ready
        self.g = {}                       # godet_id -> dict
        self.alert_keys = set()           # (godet_id, loop) of confirmations already raised
        self.new_alerts = deque(maxlen=1000)

    def on_pass(self, rec: PassRecord, evidence=None):
        self.pop.append(rec.raw)
        if len(self.pop) < MIN_POPULATION:
            self.early.append((rec, evidence))
            return
        if self.early:
            early, self.early = self.early, []
            for r, e in early:
                self._judge(r, e)
        self._judge(rec, evidence)

    def _relative(self, raw):
        p = np.fromiter(self.pop, float)
        med, p90 = np.median(p), np.percentile(p, 90)
        return float((raw - med) / max(p90 - med, 1e-3))

    def _judge(self, rec, evidence):
        rel = self._relative(rec.raw)
        g = self.g.setdefault(rec.godet_id, {"rel": deque(maxlen=self.keep), "loops": deque(maxlen=self.keep),
                                             "evidence": None, "evidence_rel": -np.inf, "state": "healthy",
                                             "confirmed_loop": None})
        g["rel"].append(rel); g["loops"].append(rec.loop)
        # Evidence = the LATEST strong pass with a retained frame (recent enough to still be in
        # the pipeline's evidence cache); before any strong pass, the strongest one seen.
        if evidence is not None and (rel >= self.pass_alarm or rel >= g["evidence_rel"]):
            g["evidence"], g["evidence_rel"] = evidence, rel
        before = g["state"]
        g["state"] = self.state_of(g)
        if g["state"] != "confirmed":
            g["confirmed_loop"] = None
        if g["state"] == "confirmed" and before != "confirmed":
            g["confirmed_loop"] = rec.loop
            key = (rec.godet_id, rec.loop)
            if key not in self.alert_keys:
                self.alert_keys.add(key)
                self.new_alerts.append({"godet_id": rec.godet_id, "loop": rec.loop,
                                        "severity": self.typical(g)})

    def typical(self, g):
        return float(np.median(g["rel"])) if g["rel"] else float("nan")

    def state_of(self, g):
        if len(g["rel"]) >= self.min_passes and self.typical(g) >= self.alarm:
            return "confirmed"
        if g["rel"] and g["rel"][-1] >= self.pass_alarm:
            return "suspect"
        return "healthy"

    def ready(self):
        return len(self.pop) >= MIN_POPULATION

    def confirmed_share(self):
        seen = [g for g in self.g.values() if len(g["rel"]) >= self.min_passes]
        return sum(g["state"] == "confirmed" for g in seen) / len(seen) if seen else 0.0

    # ---- persistence -------------------------------------------------------------------
    def snapshot(self):
        return {"pop": list(self.pop),
                "g": {str(k): {"rel": list(v["rel"]), "loops": list(v["loops"]), "evidence": v["evidence"],
                               "evidence_rel": v["evidence_rel"] if np.isfinite(v["evidence_rel"]) else None,
                               "state": v["state"], "confirmed_loop": v.get("confirmed_loop")}
                      for k, v in self.g.items()},
                "alert_keys": [list(k) for k in self.alert_keys]}

    def restore(self, snap):
        self.pop.extend(snap.get("pop", []))
        for k, v in snap.get("g", {}).items():
            self.g[int(k)] = {"rel": deque(v["rel"], maxlen=self.keep), "loops": deque(v["loops"], maxlen=self.keep),
                              "evidence": v["evidence"],
                              "evidence_rel": v["evidence_rel"] if v["evidence_rel"] is not None else -np.inf,
                              "state": v["state"], "confirmed_loop": v.get("confirmed_loop")}
        self.alert_keys = {tuple(k) for k in snap.get("alert_keys", [])}


def pass_to_dict(rec):
    return asdict(rec)
