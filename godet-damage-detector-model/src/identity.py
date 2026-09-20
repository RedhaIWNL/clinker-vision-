"""Streaming port of godet3/identity.py: every slot gets a godet number.

Batch recap: slot = absolute row index (real + virtual, one per godet); LOOP =
physical chain length (1208 here); barcode = occluded|short pattern of one loop;
godet_id = (slot - loop start) mod LOOP after barcode alignment; the lip seen as
L in slot g is R in slot g+1 (two views, same physical lip).

Streaming rules (all slot numbers below are ABSOLUTE — the slot counter never
resets; prune only drops array prefixes and tracks the base):
1. Bootstrap: Tier-2 is dark until LOOP+300 slots are observed (batch needs the
   same n-300 minimum to score a barcode). Only FULL-loop segments are scored
   (partial tails match spuriously and collapse the margin); the candidate
   window grows as slots arrive, so any true start is eventually covered.
   Later loops search ±barcode_search around the previous offset.
2. Provisional starts: loop k+1 provisionally starts at start_k + LOOP the moment
   the slot counter gets there (counting is positional; virtual slots preserve
   it). The previous head is CONFIRMED at that moment (full-loop bits available);
   the new head is VERIFIED once >=300 of its bits arrive and corrected
   retroactively if an alternative wins by margin.
3. Row emission lags one loop: godet rows for loop k emit only after loop k+1
   starts (all starts involved are final by then). Tier-2 latency is loop-scale
   anyway — alerts recompute per completed loop.
4. No confident alignment for 2 full loops -> chain_changed (master rebuild is a
   manual runbook procedure; godets are never silently renumbered).
"""
from __future__ import annotations

import numpy as np
from scipy.signal import find_peaks

VALID = ("readable", "weak", "edge")  # views used for a godet's lip


def autocorr_peak(x, lo, hi):
    """Loop length from the bit series (godet3/identity.py logic, unmasked):
    strongest autocorrelation peak in [lo, hi)."""
    x = np.asarray(x, dtype=float)
    xm = x - x.mean()
    var = xm.var() + 1e-9
    lags = np.arange(lo, hi)
    ac = np.array([(xm[:-lg] * xm[lg:]).mean() / var for lg in lags])
    pk, _ = find_peaks(ac, prominence=0.03)
    if len(pk) == 0:
        return None, 0.0
    best = pk[int(np.argmax(ac[pk]))]
    return int(lags[best]), float(ac[best])


def dilate(b, k=1):
    """Verbatim port of godet3/identity.py::dilate."""
    b = np.asarray(b).astype(bool)
    out = b.copy()
    for s in range(1, k + 1):
        out[s:] |= b[:-s]
        out[:-s] |= b[s:]
    return out


def barcode_match(master, occ, start, LOOP):
    """Verbatim port of godet3/identity.py::barcode_match: dilated correlation
    of the master barcode with the pattern starting at array index `start`.
    Partial tail segments (len >= 300) still score; shorter ones return -1."""
    seg = np.asarray(occ[start:start + LOOP])
    m = dilate(master[:len(seg)]).astype(float)
    s = dilate(seg).astype(float)
    if len(seg) < 300 or m.std() == 0 or s.std() == 0:
        return -1.0
    return float(np.corrcoef(m, s)[0, 1])


def best_offset(code, master, LOOP, center, search):
    """Best array-index offset near `center` with margin over alternatives >2 away."""
    n = len(code)
    prof = {}
    for c in range(center - search, center + search + 1):
        if c < 0 or c + 300 > n:
            continue
        prof[c] = barcode_match(master, code, c, LOOP)
    if not prof:
        return None, -1.0, 0.0
    best = max(prof, key=prof.get)
    bs = prof[best]
    others = [v for c, v in prof.items() if abs(c - best) > 2]
    margin = bs - (max(others) if others else -1.0)
    return best, bs, margin


def _fill_of(status, top, bot, lit):
    if status not in VALID:
        return np.nan
    if bot is None or top is None or lit is None:
        return np.nan
    if bot >= 0 and top >= 0 and (bot - top + 1) > 0:
        return float(lit / (bot - top + 1))
    return np.nan


def assemble_godet_row(r, nxt, gid, loop_no, nxt_loop_no=None):
    """One godet row from slot record r (left view) and its successor nxt
    (right view SlotRecord, or None). The right view counts only when it sits
    in the SAME loop (batch rule). Same fields and formulas as batch."""
    vL = r.status in VALID
    vR = nxt is not None and nxt_loop_no == loop_no and nxt.status in VALID
    same_loop = nxt is not None and nxt_loop_no == loop_no
    litL = r.lit_L if vL else np.nan
    litR = nxt.lit_R if vR else np.nan
    views = [v for v in (litL, litR) if not (v is None or (isinstance(v, float) and np.isnan(v)))]
    stL = r.straight_L if vL else np.nan
    stR = nxt.straight_R if vR else np.nan
    slL = r.slope_L if vL else np.nan
    slR = nxt.slope_R if vR else np.nan
    f1 = r.face_resid if vL else np.nan
    f2 = nxt.face_resid if vR else np.nan
    if (isinstance(f1, float) and np.isnan(f1)) and (isinstance(f2, float) and np.isnan(f2)):
        fmax = np.nan
    else:
        fmax = float(np.nanmax([f1, f2]))
    flL = _fill_of(r.status, r.top_L, r.bot_L, r.lit_L)
    flR = _fill_of(nxt.status if vR else "none", nxt.top_R if vR else None,
                   nxt.bot_R if vR else None, nxt.lit_R if vR else None)
    short_L = bool(r.short_L) if vL else False
    short_R = bool(nxt.short_R) if vR else False
    return {"godet_id": int(gid), "loop": int(loop_no), "slot": int(r.slot),
            "frame_id": r.frame_id,
            "t_left": r.t_wall, "t_right": nxt.t_wall if same_loop else np.nan,
            "status_left": r.status,
            "status_right": nxt.status if same_loop else "none",
            "lit_L": litL, "lit_R": litR, "n_views": len(views),
            "lip": min(views) if views else np.nan,
            "short_L": short_L, "short_R": short_R,
            "short_both": short_L and short_R, "short_any": short_L or short_R,
            "straight_L": stL, "straight_R": stR, "face_max": fmax,
            "slope_L": slL, "slope_R": slR, "fill_L": flL, "fill_R": flR,
            "face_resid": r.face_resid if vL else np.nan,
            "peak_left": r.peak}


class StreamingIdentity:
    """Consumes SlotRecords in slot order (via pipeline.subscribe), emits
    finalized godet rows to sinks. States: aligning -> locked (-> resyncing)."""

    def __init__(self, master, loop_len, rules):
        self.master = np.asarray(master).astype(bool)
        self.LOOP = int(loop_len)
        self.rules = rules
        self.search = int(rules["barcode_search"])
        self.min_score = float(rules["barcode_min_score"])
        self.min_margin = float(rules["barcode_min_margin"])
        self.bits = []        # per-slot barcode bits (occ|short), pruned with records
        self.records = []     # SlotRecords in slot order (pruned past 2 loops)
        self.base = None      # absolute slot number of records[0]/bits[0]
        self.starts = []      # locked loop starts (ABSOLUTE slots)
        self.scores = []
        self.margins = []
        self.locked = False
        self.resyncing = False
        self.chain_changed = False
        self.unconfident_slots = 0
        self.sinks = []
        self.loop_sinks = []  # on_loop_start(loop_no, start_slot) callbacks
        self.rows_emitted = 0
        self._fin = 0           # records index of first unfinalized slot
        self._last_bootstrap_try = -10 ** 9
        self.max_slot = None    # highest slot accepted (sender retries resend
        self.duplicates = 0     # already-seen slots: counted, never reprocessed
        self.loop_drift_est = None  # re-estimated loop length (2-loop check)

    def subscribe(self, fn):
        self.sinks.append(fn)

    def subscribe_loops(self, fn):
        self.loop_sinks.append(fn)

    def _emit_loop(self, k):
        for fn in self.loop_sinks:
            fn(k, self.starts[k])

    # -- input -----------------------------------------------------------
    def on_slot(self, rec):
        if self.max_slot is not None and rec.slot <= self.max_slot:
            self.duplicates += 1  # sender retry / redelivery: skip, stay aligned
            return
        self.max_slot = rec.slot
        if self.base is None:
            self.base = rec.slot
        self.records.append(rec)
        valid = rec.status in VALID
        self.bits.append((rec.status == "occluded")
                         or (bool(rec.short_L or rec.short_R) and valid))
        if not self.locked:
            if len(self.records) >= self.LOOP + 300 and (
                    rec.slot - self._last_bootstrap_try >= 50):
                self._last_bootstrap_try = rec.slot
                self._bootstrap()
            return
        cur = rec.slot
        if cur - self.starts[-1] >= self.LOOP and not self._provisional():
            self._confirm_head()
            self.starts.append(self.starts[-1] + self.LOOP)
            self.scores.append(float("nan"))
            self.margins.append(float("nan"))
            self._emit_loop(len(self.starts) - 1)
        self._verify_head()
        self._finalize_ready()
        self._prune()

    def _provisional(self):
        """The head start is provisional until verified (score NaN)."""
        return bool(self.scores) and isinstance(self.scores[-1], float) and np.isnan(self.scores[-1])

    # -- alignment ---------------------------------------------------------
    def _idx(self, absolute_slot):
        return absolute_slot - self.base

    def _bootstrap(self):
        # Score FULL-loop segments only (start + LOOP <= n): partial tails
        # match spuriously and collapse the margin (observed 0.09 vs 0.6+).
        # The candidate window grows as slots arrive, so a true start anywhere
        # in the loop is eventually covered; past 3 loops without lock means
        # the chain no longer matches the master.
        if len(self.records) > 3 * self.LOOP:
            self.chain_changed = True
            return
        code = np.array(self.bits)
        n = len(code)
        full = n - self.LOOP  # last start with a full segment
        best, bs, margin = best_offset(code, self.master, self.LOOP,
                                       center=full // 2, search=full // 2)
        if best is None:
            return
        if bs >= self.min_score and margin >= self.min_margin:
            s0 = self.base + int(best)
            if s0 > self.base:
                # recording began mid-loop: slots before s0 are the previous
                # loop's tail (batch convention: loop 0, partial).
                self.starts = [s0 - self.LOOP, s0]
                self.scores = [float("nan"), float(bs)]
                self.margins = [float("nan"), float(margin)]
                self.locked = True
                self._emit_loop(0)
                self._emit_loop(1)
            else:
                self.starts = [s0]
                self.scores = [float(bs)]
                self.margins = [float(margin)]
                self.locked = True
                self._emit_loop(0)
        # else: keep buffering; every 50th slot retries with a wider window.

    def _verify_head(self):
        if self.chain_changed or not self.starts:
            return
        head_idx = self._idx(self.starts[-1])
        if head_idx < 0:
            return  # post-restart: history not yet covering the head; stand down
        avail = len(self.records) - head_idx
        if avail < 300 or avail >= self.LOOP:
            return
        if avail > 300 and (self.records[-1].slot % 10):
            return  # throttle: verify at avail==300 then every 10 slots
        code = np.array(self.bits)
        best, bs, margin = best_offset(code, self.master, self.LOOP,
                                       center=head_idx, search=self.search)
        if best is None:
            return
        if (best != head_idx and bs >= self.min_score
                and margin >= self.min_margin):
            self.starts[-1] = self.base + int(best)
            self.scores[-1] = float(bs)
            self.margins[-1] = float(margin)
            self.unconfident_slots = 0
        elif bs < self.min_score or margin < self.min_margin:
            self.unconfident_slots += 10
            if self.unconfident_slots >= 2 * self.LOOP:
                self.chain_changed = True
        else:
            self.unconfident_slots = 0
            self.scores[-1] = float(bs)
            self.margins[-1] = float(margin)

    def _confirm_head(self):
        code = np.array(self.bits)
        head_idx = self._idx(self.starts[-1])
        if head_idx < 0 or len(self.records) - head_idx < self.LOOP:
            return  # post-restart: full-loop bits not yet observed; stand down
        best, bs, margin = best_offset(code, self.master, self.LOOP,
                                       center=head_idx, search=self.search)
        ok = (best == head_idx and bs >= self.min_score and margin >= self.min_margin)
        self.resyncing = not ok
        if ok:
            self.scores[-1] = float(bs)
            self.margins[-1] = float(margin)
            self.unconfident_slots = 0
            if len(self.starts) >= 3 and self.loop_drift_est is None:
                self._check_drift()
        else:
            self.unconfident_slots += self.LOOP
            if self.unconfident_slots >= 2 * self.LOOP:
                self.chain_changed = True

    def _check_drift(self):
        """Re-estimate the loop length from the bit series once 2+ loops are
        locked; a >3% deviation means the chain changed under us (godets added
        or removed): report, never silently renumber."""
        code = np.array(self.bits)
        est, r = autocorr_peak(code, 900, 1500)
        self.loop_drift_est = est
        if est is not None and abs(est - self.LOOP) / self.LOOP > 0.03:
            self.chain_changed = True

    # -- godet rows ----------------------------------------------------------
    def _loop_of(self, absolute_slot):
        import bisect

        k = bisect.bisect_right(self.starts, absolute_slot) - 1
        return k if k >= 0 else None

    def _finalize_ready(self):
        """Emit rows for slots in CLOSED loops only (loop index < head loop):
        every start involved is final, so emitted rows never change. Emission
        additionally lags until slot i+2 exists: an occluded slot retro-marks
        its two predecessors `edge`, and those marks must be in place before
        the rows are assembled (batch sees the whole run; the stream catches up
        2 slots later). Scans forward from the first unfinalized slot (O(n)
        total, not O(n^2))."""
        if len(self.starts) < 2:
            return
        recs = self.records
        i = self._fin
        while i + 2 < len(recs):
            r = recs[i]
            if getattr(r, "row_done", False):
                i += 1
                continue
            k = self._loop_of(r.slot)
            if k is None or k >= len(self.starts) - 1:
                break  # later slots aren't ready either (ordered) — resume here
            gid = (r.slot - self.starts[k]) % self.LOOP
            nxt = recs[i + 1]
            row = assemble_godet_row(r, nxt, gid, k, self._loop_of(nxt.slot))
            r.row_done = True
            self.rows_emitted += 1
            for fn in self.sinks:
                fn(row)
            i += 1
        self._fin = i

    def _prune(self):
        """Drop raw records older than 2 locked loops (godet rows carry the
        downstream state). Arrays stay aligned via base."""
        if len(self.starts) < 3:
            return
        cutoff = self.starts[-3]
        n_pop = 0
        while self.records and self.records[0].slot < cutoff:
            self.records.pop(0)
            self.bits.pop(0)
            self.base += 1
            n_pop += 1
        self._fin = max(0, self._fin - n_pop)

    def flush(self):
        """End-of-stream only: emit rows for every unfinalized slot using the
        current (possibly provisional head) starts, mirroring batch which
        numbers all slots including the trailing partial loop."""
        recs = self.records
        for i in range(self._fin, len(recs) - 1):
            r = recs[i]
            if getattr(r, "row_done", False):
                continue
            k = self._loop_of(r.slot)
            if k is None:
                continue
            gid = (r.slot - self.starts[k]) % self.LOOP
            row = assemble_godet_row(r, recs[i + 1], gid, k,
                                     self._loop_of(recs[i + 1].slot))
            r.row_done = True
            self.rows_emitted += 1
            for fn in self.sinks:
                fn(row)
        # trailing slot: no successor exists (end of stream) — single-view row,
        # exactly like batch.
        if recs and not getattr(recs[-1], "row_done", False):
            r = recs[-1]
            k = self._loop_of(r.slot)
            if k is not None:
                gid = (r.slot - self.starts[k]) % self.LOOP
                row = assemble_godet_row(r, None, gid, k)
                r.row_done = True
                self.rows_emitted += 1
                for fn in self.sinks:
                    fn(row)
        self._fin = len(recs) - 1

    # -- state for store/health ----------------------------------------------
    def snapshot(self):
        tail = self.records[-(2 * self.LOOP + 80):]
        return {"starts": list(self.starts), "scores": list(self.scores),
                "margins": list(self.margins),
                "locked": self.locked, "resyncing": self.resyncing,
                "chain_changed": self.chain_changed,
                "unconfident_slots": self.unconfident_slots,
                "rows_emitted": self.rows_emitted,
                "max_slot": self.max_slot, "duplicates": self.duplicates,
                "loop_drift_est": self.loop_drift_est,
                "tail": [r.to_dict() for r in tail]}

    def restore(self, snap):
        self.starts = list(snap["starts"])
        self.scores = list(snap["scores"])
        self.margins = list(snap["margins"])
        self.locked = snap["locked"]
        self.resyncing = snap["resyncing"]
        self.chain_changed = snap["chain_changed"]
        self.unconfident_slots = snap.get("unconfident_slots", 0)
        self.rows_emitted = snap.get("rows_emitted", 0)
        self.max_slot = snap.get("max_slot")
        self.duplicates = snap.get("duplicates", 0)
        self.loop_drift_est = snap.get("loop_drift_est")

    def restore_tail(self, tail_dicts):
        """Re-ingest unfinalized tail records after a restart (row_done flags
        travel with them: emitted rows are never re-emitted, pending rows
        finalize normally as new slots arrive)."""
        from src.pipeline import SlotRecord

        for d in tail_dicts:
            rec = SlotRecord.from_dict(d)
            if self.base is None:
                self.base = rec.slot
            self.records.append(rec)
            valid = rec.status in VALID
            self.bits.append((rec.status == "occluded")
                             or (bool(rec.short_L or rec.short_R) and valid))
        self._fin = 0
