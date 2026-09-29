"""Godet outside damage for the new Camera 1: plates cut at their real joints, scored by a CNN + a cut cue.

Streaming port of the calibration repo's cam4/plates.py (joints), cam4/plate_cnn_v2_train.py (model) and
cam4/valley_cut.py (cut cue). Verified offline by the user on the whole chain (2026-09-29): 229 of 230
judgements right (types included), 0 missed in 20 random godets (calibration repo CAM1_NEW_PLAN.md §7).

Per batch of scored strip columns (grey, strip rows row0..row1):
1. joints: all joints of the buffered strip chosen together (dynamic programming: strong vertical edges
   AND ~one plate apart); joints older than COMMIT_LAG columns are final;
2. each final plate (between two joints) waits until the chain map has placed its centre, then gets the
   godet number of the slot its centre falls in (slot borders on the joints: bundle phase), one more than
   the previous plate if the map gives the same number twice;
3. score: the plate picture (280 x 240, grey, centred) through the bundle's 3 ONNX models (outputs:
   damaged, cut, out of line; averaged), and the valley cut score (a thin dark line with brighter metal
   above AND below), relative to the recent population of plates (the light changes the level);
4. a godet is confirmed when it has >= min_passes passes and the combination (logistic, bundle weights)
   of the medians over its passes is >= threshold. Type: cut / out of line / both from the medians.
"""
from __future__ import annotations

import threading
import uuid
from collections import OrderedDict, deque

import cv2
import numpy as np
from scipy.ndimage import maximum_filter1d

COMMIT_LAG = 700            # columns: joints this far behind the newest column are final
BUFFER = 6000               # strip columns kept
POPULATION = 1500           # plates in the valley-score population


def edge_signal(g, rows):
    x = np.abs(cv2.Sobel(cv2.GaussianBlur(g, (5, 5), 0), cv2.CV_32F, 1, 0, ksize=3))[rows[0]:rows[1]].mean(0)
    return cv2.GaussianBlur(x[None], (9, 1), 0)[0]


def best_joints(e, start, lo, hi, step, lam, skip_cost):
    """Joints from index `start` (a known joint) to the end of e, chosen together (see module doc)."""
    en = e / (np.median(e) + 1e-6)
    cand = np.flatnonzero(e >= maximum_filter1d(e, 21))
    cand = np.unique(np.r_[start, cand[(cand > start) & (en[cand] > 0.8)]])
    n = len(cand); best = np.full(n, -np.inf); prev = np.full(n, -1); dbl = np.zeros(n, bool)
    best[0] = 0.0
    j0 = 0
    for i in range(1, n):
        c = cand[i]
        while cand[j0] < c - 2 * hi:
            j0 += 1
        for j in range(j0, i):
            d = c - cand[j]
            if lo <= d <= hi:
                v = best[j] + en[c] - lam * (d - step) ** 2; two = False
            elif 2 * lo <= d <= 2 * hi:
                v = best[j] + en[c] - lam * (d / 2 - step) ** 2 - skip_cost; two = True
            else:
                continue
            if v > best[i]:
                best[i], prev[i], dbl[i] = v, j, two
    ok = np.isfinite(best)
    if ok.sum() <= 1:
        return []
    i = int(np.flatnonzero(ok)[np.argmax(cand[ok])])          # the path reaching furthest
    out = []
    while i > 0:
        out.append(int(cand[i]))
        if dbl[i] and prev[i] >= 0:
            out.append(int((cand[i] + cand[prev[i]]) // 2))
        i = prev[i]
    return sorted(out)


def valley_score(g):
    v = []
    gb = cv2.GaussianBlur(g, (3, 3), 0)
    for d in (3, 4, 5):
        up = np.roll(gb, d, 0); dn = np.roll(gb, -d, 0)
        x = np.clip(np.minimum(up - gb, dn - gb), 0, None); x[:d] = 0; x[-d:] = 0; v.append(x)
    h = cv2.blur(np.maximum.reduce(v), (31, 1))
    return float(np.percentile((h / (g.mean() + 10.0)).max(0), 60))


class PlateTracker:
    def __init__(self, bundle, ident, camera_id, retain=None):
        self.c = bundle.plates; self.ident = ident; self.cam = camera_id; self.retain = retain
        self.L = int(bundle.ident["loop_cols"]); self.P = float(bundle.ident["pitch"]); self.N = int(bundle.ident["godets"])
        self.phase = float(bundle.ident["phase"])
        self.nets = [cv2.dnn.readNetFromONNX(str(f)) for f in bundle.plate_models]
        self.row0 = None
        self.buf = None; self.buf0 = 0                       # grey strip buffer (rows row0..row1), first column
        self.edge = np.zeros(0, np.float32)
        self.last_joint = None
        self.pending = deque()                               # (a, b) plates waiting for the map
        self.prev = None                                     # (godet, loop) of the previous plate
        self.pop = deque(maxlen=POPULATION)
        self.g = OrderedDict()                               # godet -> {"passes": deque, "state", ...}
        self.lock = threading.Lock()
        self.counters = {"plates_total": 0, "plates_estimated_joint_total": 0, "plates_unplaced_total": 0,
                         "plate_godets_confirmed": 0}

    # ---- input: scored strip columns (grey, rows row0..row1) -------------------------------------
    def on_strip(self, cols, g, row0):
        if len(cols) == 0:
            return
        self.row0 = row0
        g = g.astype(np.float32)
        if self.buf is None or cols[0] != self.buf0 + self.buf.shape[1]:
            self.buf, self.buf0, self.edge, self.last_joint = g, int(cols[0]), np.zeros(0, np.float32), None
        else:
            self.buf = np.concatenate([self.buf, g], 1)
        er = [r - row0 for r in self.c["edge_rows"]]
        self.edge = edge_signal(self.buf, er)                # recomputed on the buffer (edges need both sides)
        c = self.c["dp"]
        if self.last_joint is None:
            self.last_joint = self.buf0 + int(np.argmax(self.edge[:400]))
        js = best_joints(self.edge, self.last_joint - self.buf0, c["lo"], c["hi"], c["step"], c["lam"], c["skip_cost"])
        end = self.buf0 + self.buf.shape[1]
        a = self.last_joint
        for j in js:
            J = self.buf0 + j
            if J > end - COMMIT_LAG:
                break
            if J > a:
                self.pending.append((a, J)); a = J
        self.last_joint = a
        self._score_pending()
        if self.buf.shape[1] > BUFFER:                       # keep what the next joints and crops need
            cut = self.buf.shape[1] - BUFFER
            self.buf = self.buf[:, cut:]; self.buf0 += cut; self.edge = self.edge[cut:]

    # ---- number, crop, score --------------------------------------------------------------------
    def _position(self, col):
        an = self.ident.anchors
        if not self.ident.locked or len(an) < 2 or an[-1][0] < col:
            return None
        cs = np.array([x[0] for x in an], float); ps = np.array([x[1] for x in an], float)
        k = int(np.clip(np.searchsorted(cs, col), 1, len(cs) - 1))
        return ps[k - 1] + (col - cs[k - 1]) * (ps[k] - ps[k - 1]) / max(cs[k] - cs[k - 1], 1.0)

    def _score_pending(self):
        W = self.c["crop_w"]; r0, r1 = [r - self.row0 for r in self.c["rows"]]
        v0, v1 = [r - self.row0 for r in self.c["valley_rows"]]; sh = self.c["valley_shift"]
        while self.pending:
            a, b = self.pending[0]
            cen = (a + b) // 2
            if cen + W // 2 >= self.buf0 + self.buf.shape[1]:
                return                                       # crop not complete yet
            pos = self._position(cen)
            if pos is None:
                if not self.ident.locked:
                    self.pending.popleft(); self.counters["plates_unplaced_total"] += 1; self.prev = None
                    continue
                return                                       # the map has not placed it yet
            self.pending.popleft()
            q = pos - self.phase
            godet = int(np.floor(q / self.P)) % self.N
            loop = int(max(np.floor(q / self.L) - (self.ident.loop_base or 0), 0) + self.ident.loop_offset)
            if self.prev is not None and self.prev == (godet, loop):
                godet = (godet + 1) % self.N                 # the map gave the same number twice
            self.prev = (godet, loop)
            x0 = cen - W // 2 - self.buf0
            if x0 < 0:
                continue
            crop = np.clip(self.buf[r0:r1, x0:x0 + W], 0, 255)
            blob = ((crop / 255.0 - 0.45) / 0.25).astype(np.float32)
            blob = np.repeat(blob[None, None], 3, 1)
            out = []
            for net in self.nets:
                net.setInput(blob); out.append(1 / (1 + np.exp(-net.forward()[0])))
            p = np.mean(out, 0)
            va = valley_score(self.buf[v0:v1, a + sh - self.buf0:b + sh - self.buf0])
            self.pop.append(va)
            self._on_plate(godet, loop, p, va, cen)

    # ---- per godet history and rule --------------------------------------------------------------
    def _on_plate(self, godet, loop, p, va, col):
        self.counters["plates_total"] += 1
        pop = np.fromiter(self.pop, float)
        med, p90 = (np.median(pop), np.percentile(pop, 90)) if len(pop) >= 200 else (0.0, 1.0)
        vrel = (va - med) / max(p90 - med, 1e-6)
        s = self.c["stack"]
        with self.lock:
            d = self.g.setdefault(godet, {"passes": deque(maxlen=self.c["keep_passes"]), "state": "healthy",
                                          "evidence": None, "confirmed_loop": None, "last_loop": loop})
            d["passes"].append((float(p[0]), float(p[1]), float(p[2]), float(vrel), int(col), int(loop)))
            d["last_loop"] = loop
            m = np.median(np.array([x[:4] for x in d["passes"]]), 0)
            lg = lambda x: float(np.log(np.clip(x, 1e-4, 1 - 1e-4) / (1 - np.clip(x, 1e-4, 1 - 1e-4))))
            f = np.array([lg(m[0]), lg(m[1]), lg(m[2]), m[3]])
            score = float(1 / (1 + np.exp(-(((f - np.array(s["mu"])) / np.array(s["sd"])) @ np.array(s["w"]) + s["b"]))))
            d["score"] = score; d["cut"] = float(m[1]); d["out_of_line"] = float(m[2])
            one = float(1 / (1 + np.exp(-(((np.array([lg(p[0]), lg(p[1]), lg(p[2]), vrel]) - np.array(s["mu"]))
                                            / np.array(s["sd"])) @ np.array(s["w"]) + s["b"]))))
            if one >= 0.75 * s["threshold"] and self.retain is not None:
                ev = self.retain(col)                        # a frame that shows this plate, for the alert
                if ev is not None:
                    d["evidence"] = ev
            if len(d["passes"]) >= self.c["min_passes"] and score >= s["threshold"]:
                if d["state"] != "confirmed":
                    d["state"] = "confirmed"; d["confirmed_loop"] = loop
                    self.counters["plate_godets_confirmed"] += 1
            elif d["state"] == "confirmed" and len(d["passes"]) >= 4 and score < 0.5 * s["threshold"]:
                d["state"] = "healthy"                       # cleared (repaired)

    def damage_type(self, d):
        c, o = d.get("cut", 0) >= 0.5, d.get("out_of_line", 0) >= 0.5
        return "cut + out of line" if c and o else "cut" if c else "out of line" if o else (
            "cut" if d.get("cut", 0) > d.get("out_of_line", 0) else "out of line")

    def confirmed(self):
        with self.lock:
            return {g: dict(d, passes=list(d["passes"])) for g, d in self.g.items() if d["state"] == "confirmed"}

    # ---- persistence ------------------------------------------------------------------------------
    def snapshot(self):
        with self.lock:
            return {"godets": {str(g): {k: (list(v) if k == "passes" else v) for k, v in d.items()} for g, d in self.g.items()},
                    "counters": dict(self.counters)}

    def restore(self, snap):
        for g, d in (snap or {}).get("godets", {}).items():
            d["passes"] = deque([tuple(x) for x in d.get("passes", [])], maxlen=self.c["keep_passes"])
            self.g[int(g)] = d
        self.counters.update((snap or {}).get("counters", {}))
