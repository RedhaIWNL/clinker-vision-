"""CAM-4 streaming front end: chain odometer + unrolled chain + overlap-fault detector.

Per frame (in sequence order): measure how far the chain moved (phase correlation on a
plate-only patch, against the 1..4 previous frames), paste a thin slit across the plate
band into the unrolled chain at that position, and finalise columns the slit has passed.
Finalised columns are scored in blocks (the detector needs ~1100 columns of context each
side), then handed to identity as (column, fingerprint, severity).

Documented deviations from the batch pipeline (cam4/unroll.py + cam4/detect.py):
1. Grey decode: the batch mosaic was colour and converted to grey afterwards; bilinear
   interpolation and the grey conversion commute up to rounding.
2. Only mosaic rows 40..280 are built (all the detector reads).
3. The Otsu brightness blocks are aligned to absolute column multiples of 400 (batch:
   relative to each 20000-column chunk). Replay gate: see tests/test_cam4.py.

Camera 3 (bundle detector mode "neighbours", calibration repo cam4/neighbours.py): the damage
score compares each plate with its neighbours on a darkness map, the fingerprint is the brightness
of image bands, and an empty conveyor (bare godet floors repeating) is reported and not judged.
Scores are computed every NB_STEP absolute columns and held, as in the batch tool.
"""
from __future__ import annotations

import time
from collections import deque

import cv2
import numpy as np
from scipy.ndimage import median_filter, uniform_filter1d

# Wheel (galet) signals, as the calibration repo's cam4/wheels.py (mosaic rows are absolute).
WHEEL_DEFAULTS = {"stripe_rows": [326, 329], "stripe_above": [320, 323], "stripe_below": [332, 335],
                  "hidden_frac": 0.7, "min_quality": 0.3, "min_step": 4.0, "min_contrast_frac": 0.5,
                  "max_hidden_frac": 0.6, "hidden_window": 801, "strip_rows": [140, 340]}

ROW0, ROW1 = 40, 340          # CAM-4 mosaic rows kept: damage detector 40..280, wheels (rail) down to 340
DET_H = 280 - ROW0            # the damage detector sees exactly mosaic rows 40..280 (as calibrated)
NB_STEP = 30                  # neighbour score every 30 absolute columns (batch: the same grid)
RING = 4096                   # accumulation ring (columns); the slit writes ~20 columns ahead
FINAL_MARGIN = 20             # a column is final once the slit is this far past it
CTX = 1100                    # detector context each side (median filters over 5 pitches)
BLOCK_COLS = 1000             # score in blocks of this many new columns


def _grid(bundle, a_vals, b_vals):
    a = np.asarray(a_vals, np.float32)[None, :]
    b = np.asarray(b_vals, np.float32)[:, None]
    x = bundle.p0[0] + a * bundle.u[0] + b * bundle.v[0]
    y = bundle.p0[1] + a * bundle.u[1] + b * bundle.v[1]
    return x.astype(np.float32), y.astype(np.float32)


# ---- detector (vendored from cam4/detect.py; input is the grey band rows 40..280) ----------
def column_signals(g, det):
    """Edge row (local to row 40) and closed-in shadow length per column of a grey band."""
    g = cv2.GaussianBlur(g.astype(np.float32), (5, 5), 0)
    H, W = g.shape
    thr = np.zeros(W, np.float32)
    for b0 in range(0, W, det["block"]):
        blk = np.clip(g[:, b0:b0 + det["block"]], 0, 255).astype(np.uint8)
        t, _ = cv2.threshold(blk, 0, 255, cv2.THRESH_BINARY + cv2.THRESH_OTSU)
        thr[b0:b0 + det["block"]] = t
    bright = g > thr[None]
    run4 = bright[:-3] & bright[1:-2] & bright[2:-1] & bright[3:]
    top = np.where(run4.any(0), run4.argmax(0), H).astype(np.float32)
    pitch = det["pitch"]
    base = median_filter(top, 3 * pitch | 1)
    top = np.where(base - top > det["pole_px"], base, top)
    ti = top.astype(int)
    rb = ti[None] + np.arange(40, 140)[:, None]
    vals = np.where(rb < H, np.take_along_axis(g, np.clip(rb, 0, H - 1), 0), np.nan)
    with np.errstate(all="ignore"):
        import warnings
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", RuntimeWarning)
            body = np.nan_to_num(np.nanmedian(vals, 0), nan=255.0)
    rr = ti[None] + np.arange(0, det["shadow_depth"])[:, None]
    seg = np.where(rr < H, np.take_along_axis(g, np.clip(rr, 0, H - 1), 0), 255.0)
    dk = seg < det["dark_frac"] * body[None]
    L = seg.shape[0]
    first_dark = np.where(dk.any(0), dk.argmax(0), L)
    lit_after = ~dk & (np.arange(L)[:, None] >= first_dark[None])
    end = np.where(lit_after.any(0), lit_after.argmax(0), L)
    closed = (first_dark >= 2) & (end < L)
    wedge = np.where(closed, end - first_dark, 0).astype(np.float32)
    t0 = det["texture_rows"][0] - ROW0                         # CAM-4 only (rows from 40)
    texture = np.abs(cv2.Sobel(g[t0:], cv2.CV_32F, 1, 0)).mean(0)
    return top, wedge, texture


def darkness(g):
    """How much darker than the local plate surface (bright level over ~9 rows x 25 columns)."""
    level = cv2.dilate(cv2.GaussianBlur(g, (5, 5), 0), np.ones((9, 25), np.uint8))
    return np.clip(level - g, 0, None)


def neighbour_scores(g, row0, xs, det):
    """Plate-vs-neighbours score at window columns xs (g: grey window, first row = mosaic row0)."""
    r0, r1 = det["rows"][0] - row0, det["rows"][1] - row0
    hw, sx, sy, P = det["half"], det["shift_cols"], det["shift_rows"], det["pitch"]
    g0 = g
    if det.get("image") == "darkness":
        g = g.copy(); g[r0 - 20:r1 + 20] = darkness(g[r0 - 20:r1 + 20])
    out = np.full(len(xs), np.nan, np.float32)
    for i, x in enumerate(xs):
        if g0[r0:r1, x - hw:x + hw].std() < 1.0:
            continue
        tpl = g[r0:r1, x - hw:x + hw]
        best = []
        for k in (-2, -1, 1, 2):
            xn = int(round(x + k * P))
            if xn - hw - sx < 0 or xn + hw + sx > g.shape[1]:
                continue
            win = g[r0 - sy:r1 + sy, xn - hw - sx:xn + hw + sx]
            if g0[r0 - sy:r1 + sy, xn - hw - sx:xn + hw + sx].std() < 1.0 or win.std() < 1e-3:
                continue
            best.append(float(cv2.matchTemplate(win, tpl, cv2.TM_CCOEFF_NORMED).max()))
        if len(best) >= 3:
            best.sort()
            out[i] = 1.0 - (best[-2] if det.get("agg") == "second" else float(np.mean(best)))
    return out


def repeat_scores(g, row0, xs, det):
    """How much the band above the plates repeats 1 and 2 godets on (bare godet floors = empty)."""
    r0, r1 = det["load_rows"][0] - row0, det["load_rows"][1] - row0
    hw, P = det["load_half"], det["pitch"]
    out = np.full(len(xs), np.nan, np.float32)
    for i, x in enumerate(xs):
        tpl = g[r0:r1, x - hw:x + hw]
        if tpl.std() < 1.0:
            continue
        v = []
        for k in (1, 2):
            xn = int(round(x + k * P))
            win = g[max(0, r0 - 6):r1 + 6, xn - hw - 10:xn + hw + 10]
            if win.shape[1] == 2 * hw + 20 and win.std() >= 1.0:
                v.append(float(cv2.matchTemplate(win, tpl, cv2.TM_CCOEFF_NORMED).max()))
        if v:
            out[i] = float(np.mean(v))
    return out


def scores(top, wedge, det):
    pitch = det["pitch"]
    base = median_filter(top, 3 * pitch | 1)
    elev = np.clip(base - top, 0, det["pole_px"])
    raised = uniform_filter1d((elev > det["raise_px"]).astype(np.float32), pitch // 3)
    shadow = uniform_filter1d(wedge, pitch // 3)
    shadow_rel = shadow - median_filter(shadow, 5 * pitch | 1)
    return shadow_rel / 4 + 3 * raised


class Cam4Stream:
    """One instance per CAM-4 dense stream. Call on_gray/on_frame in sequence order and
    on_gap(n) for frames that never arrived. Column batches go to subscribers:
    fn(cols, fp_raw (2 x n), severity (n), top (n), missing (n bool)); and to wheel subscribers:
    fn(cols, hidden (n bool: rail stripe hidden), readable (n bool), strip (rows x n uint8))."""

    def __init__(self, bundle):
        self.b = bundle
        a0, a1 = bundle.odo_a; b0, b1 = bundle.odo_b
        self.ox, self.oy = _grid(bundle, np.arange(a0, a1), np.arange(b0, b1))
        self.win = cv2.createHanningWindow((self.ox.shape[1], self.ox.shape[0]), cv2.CV_32F)
        self.A = np.arange(*bundle.slit_a)
        self.row0, self.row1 = tuple(getattr(bundle, "strip_rows", None) or (ROW0, ROW1))
        self.mode = bundle.det.get("mode", "overlap")          # "overlap" (CAM-4) | "neighbours" (CAM-3)
        rows_b = np.arange(self.row0, self.row1) - bundle.slit_half
        self.sx, self.sy = _grid(bundle, self.A, rows_b)       # (rows, len(A))
        self.wts = (1 - np.abs(self.A) / 7.0).astype(np.float32)
        H = self.row1 - self.row0
        self.acc = np.zeros((H, RING), np.float32)
        self.cnt = np.zeros(RING, np.float32)
        self.bad = np.zeros(RING, bool)           # written by a frame with a poor match / crawling chain
        self.wh = dict(WHEEL_DEFAULTS, **(getattr(bundle, "wheels", None) or {}))
        self.idx = 0                   # stream frame index (gaps advance it)
        self.s = None                  # chain position of the current frame
        self.hist = {}                 # idx -> (patch, s) for the last max(baselines) frames
        self.steps = deque(maxlen=25)
        self.quals = deque(maxlen=250)
        self.recent_steps = deque(maxlen=50)
        self.next_final = 0            # next column to finalise
        self.fin = np.zeros((H, 0), np.float32)   # finalised columns [fin0, next_final)
        self.fin0 = 0
        self.fin_missing = np.zeros(0, bool)
        self.fin_bad = np.zeros(0, bool)
        self.done = CTX                # next column to score
        self.sinks = []
        self.wheel_sinks = []
        self.plate_sinks = []
        self.counters = {"frames_total": 0, "columns_total": 0, "stopped_frames_total": 0,
                         "weak_frames_total": 0, "gap_frames_total": 0, "empty_columns_total": 0,
                         "repeated_frames_total": 0}
        self.empty_recent = deque(maxlen=20)       # share of empty columns in the latest scored blocks
        self.last_jpeg = None
        self.arrivals = deque(maxlen=250)          # (wall time, repeated?) of the latest frames

    def subscribe(self, fn):
        self.sinks.append(fn)

    def subscribe_wheels(self, fn):
        self.wheel_sinks.append(fn)

    def subscribe_plates(self, fn):
        """New Camera 1: fn(cols, grey strip rows row0..row1 (n columns), row0) for the plate tracker."""
        self.plate_sinks.append(fn)

    # ---- per frame -------------------------------------------------------------------
    def on_frame(self, jpeg: bytes, frame_id=None, sequence_no=None):
        # A picture byte-identical to the previous one is not the chain standing still (a real image
        # always has sensor noise): the camera stream lost frames and the decoder repeated the last one
        # (plant server 2026-09-30, model too busy: 4 of 5 frames repeated, read as "conveyor stopped").
        # Counted as a lost frame: the chain keeps its recent speed.
        repeated = self.last_jpeg is not None and len(jpeg) == len(self.last_jpeg) and jpeg == self.last_jpeg
        self.arrivals.append((time.monotonic(), repeated))
        if repeated:
            self.counters["repeated_frames_total"] += 1
            self.on_gap(1)
            return {"frame_id": frame_id, "chain_step": float(np.median(self.steps)) if self.steps else 0.0,
                    "chain_pos": self.s or 0.0, "match_quality": self.quals[-1] if self.quals else 0.0,
                    "chain_status": self.chain_status()}, True
        self.last_jpeg = jpeg
        img = cv2.imdecode(np.frombuffer(jpeg, dtype=np.uint8), cv2.IMREAD_GRAYSCALE)
        if img is None or img.size == 0:
            return None, False
        if (img.shape[1], img.shape[0]) != tuple(self.b.frame_size):
            return None, False
        return self.on_gray(img, frame_id, sequence_no), True

    def on_gray(self, g, frame_id=None, sequence_no=None):
        patch = cv2.remap(g, self.ox, self.oy, cv2.INTER_LINEAR).astype(np.float32)
        i = self.idx
        est, w, qual = [], [], 0.0
        for k in self.b.baselines:
            if i - k not in self.hist:
                continue
            p_prev, s_prev = self.hist[i - k]
            (da, db), r = cv2.phaseCorrelate(p_prev, patch, self.win)
            if k == 1:
                qual = float(r)
            if r > 0.2 and abs(db) < 3:
                est.append(s_prev + da); w.append(r * k)
        if self.s is None:
            s_new = 0.0
        elif est:
            s_new = float(np.average(est, weights=w))
        else:
            s_new = self.s + (float(np.median(self.steps)) if self.steps else 0.0)
        step = 0.0 if self.s is None else s_new - self.s
        if self.s is not None:
            self.steps.append(step)
        self.s = s_new
        self.hist[i] = (patch, s_new)
        self.hist.pop(i - max(self.b.baselines), None)
        self.quals.append(qual)
        self.recent_steps.append(step)
        self.counters["frames_total"] += 1
        if abs(step) < 1.0:
            self.counters["stopped_frames_total"] += 1
        if qual < 0.2 and i > 0:
            self.counters["weak_frames_total"] += 1
        bad = (qual < self.wh["min_quality"]) or (step < self.wh["min_step"])
        self._paste(g, s_new, bad)
        self.idx += 1
        return {"frame_id": frame_id, "chain_step": step, "chain_pos": s_new,
                "match_quality": qual, "chain_status": self.chain_status()}

    def on_gap(self, n_missing: int):
        """Frames that never arrived: the chain kept moving at about the recent speed.
        The columns they would have filled stay empty and are marked missing."""
        step = float(np.median(self.steps)) if self.steps else 0.0
        self.idx += n_missing
        if self.s is not None:
            self.s += step * n_missing
        self.counters["gap_frames_total"] += n_missing

    def arrival(self):
        """(frames per second arriving, share of repeated pictures) over the latest frames; None early."""
        a = list(self.arrivals)
        if len(a) < 50:
            return None
        span = a[-1][0] - a[0][0]
        fps = (len(a) - 1) / span if span > 0 else float("inf")
        return fps, sum(r for _, r in a) / len(a)

    def chain_status(self):
        """0 moving, 1 weak view (low match quality), 2 stopped."""
        if len(self.recent_steps) >= 25 and float(np.median(self.recent_steps)) < 1.0:
            return 2
        if len(self.quals) >= 25 and float(np.median(list(self.quals)[-25:])) < 0.25:
            return 1
        return 0

    # ---- unrolled chain ---------------------------------------------------------------
    def _paste(self, g, s, bad=False):
        lines = cv2.remap(g, self.sx, self.sy, cv2.INTER_LINEAR).astype(np.float32)
        cols = np.round(s - self.A).astype(int) + self.b.pad
        lo = int(cols.min())
        self._finalise(lo - FINAL_MARGIN)
        for k, c in enumerate(cols):
            if c < self.next_final or c >= self.next_final + RING:
                continue                                    # late write / wild jump: ignore
            j = c % RING
            self.acc[:, j] += lines[:, k] * self.wts[k]
            self.cnt[j] += self.wts[k]
            self.bad[j] |= bad

    def _finalise(self, upto):
        if upto <= self.next_final:
            return
        upto = min(upto, self.next_final + RING)
        idx = np.arange(self.next_final, upto) % RING
        cnt = self.cnt[idx]
        cols = self.acc[:, idx] / np.maximum(cnt, 1e-6)[None]
        missing = cnt == 0
        bad = self.bad[idx].copy()
        self.acc[:, idx] = 0; self.cnt[idx] = 0; self.bad[idx] = False
        self.fin = np.concatenate([self.fin, cols], 1)
        self.fin_missing = np.concatenate([self.fin_missing, missing])
        self.fin_bad = np.concatenate([self.fin_bad, bad])
        self.counters["columns_total"] += upto - self.next_final
        self.next_final = upto
        self._score_blocks()

    def _score_blocks(self):
        while self.next_final - self.done >= CTX + BLOCK_COLS:
            a = self.done - CTX                              # window start (absolute column)
            end = self.next_final
            win = self.fin[:, a - self.fin0:end - self.fin0]
            e = end - CTX                                    # emit [done, e)
            sl = slice(self.done - a, e - a)
            miss = self.fin_missing[self.done - self.fin0:e - self.fin0]
            cols = np.arange(self.done, e)
            if self.mode == "plates":                        # new Camera 1: damage judged per plate (plates.py)
                idn = self.b.ident
                fp = np.stack([win[r0 - self.row0:r1 - self.row0].mean(0) for r0, r1 in idn["fingerprint_bands"]])
                fp = (fp - cv2.blur(fp, (301, 1))).astype(np.float32)[:, sl]
                zero = np.zeros(len(cols), np.float32)
                for fn in self.sinks:
                    fn(cols, fp, zero, zero, miss)
                for fn in self.plate_sinks:
                    fn(cols, win[:, sl], self.row0)
            elif self.mode == "neighbours":
                fp, sev, top, empty = self._neighbour_block(win, a, cols)
                self.counters["empty_columns_total"] += int(empty.sum())
                self.empty_recent.append(float(empty.mean()))
                miss = miss | empty                          # an empty conveyor is not judged
                for fn in self.sinks:
                    fn(cols, fp, sev, top, miss)
            else:
                top, wedge, texture = column_signals(win[:DET_H], self.b.det)
                sev = scores(top, wedge, self.b.det)
                fp = np.stack([top, texture]).astype(np.float32)
                fp = fp - cv2.blur(fp, (301, 1))
                for fn in self.sinks:
                    fn(cols, fp[:, sl], sev[sl], top[sl], miss)
            if self.wheel_sinks:
                bad = self.fin_bad[a - self.fin0:end - self.fin0]
                hidden, readable, strip = self._wheel_signals(win, bad, self.fin_missing[a - self.fin0:end - self.fin0])
                for fn in self.wheel_sinks:
                    fn(cols, hidden[sl], readable[sl], strip[:, sl])
            self.done = e
            keep_from = self.done - CTX                      # trim what no window needs again
            if keep_from > self.fin0:
                self.fin = self.fin[:, keep_from - self.fin0:]
                self.fin_missing = self.fin_missing[keep_from - self.fin0:]
                self.fin_bad = self.fin_bad[keep_from - self.fin0:]
                self.fin0 = keep_from

    def _neighbour_block(self, win, a, cols):
        """Camera 3: fingerprint (image bands), neighbour score (held over NB_STEP), empty guard."""
        det, idn = self.b.det, self.b.ident
        fp = np.stack([win[r0 - self.row0:r1 - self.row0].mean(0) for r0, r1 in idn["fingerprint_bands"]])
        fp = (fp - cv2.blur(fp, (301, 1))).astype(np.float32)[:, cols[0] - a:cols[-1] + 1 - a]
        grid = NB_STEP * np.floor((cols + NB_STEP // 2) / NB_STEP).astype(int)   # batch: [c-15, c+15) -> c
        gu, inv = np.unique(grid, return_inverse=True)
        sev = neighbour_scores(win, self.row0, gu - a, det)[inv]
        # empty: median over ~10 godets of the repetition of the band above the plates
        step = 4 * NB_STEP
        xs = np.arange(step * int(np.ceil((a + 300) / step)), a + win.shape[1] - 300, step)
        rep = repeat_scores(win, self.row0, xs - a, det)
        half = 5 * det["pitch"]
        level = np.array([np.nanmedian(np.nan_to_num(rep[np.abs(xs - c) <= half], nan=0.0))
                          if np.any(np.abs(xs - c) <= half) else 0.0 for c in cols])
        empty = level >= det["max_repeat"]
        top = np.full(len(cols), det["spot_row"] - 25 - self.row0, np.float32)
        return fp, np.nan_to_num(sev, nan=0.0), top, empty

    def empty_share(self):
        """Share of the recently scored chain that was an empty conveyor (not judged)."""
        return float(np.mean(self.empty_recent)) if self.empty_recent else 0.0

    def _wheel_signals(self, win, bad, missing):
        """Rail stripe hidden / readable per column over a scoring window (grey only)."""
        w = self.wh
        if w.get("mode") == "disc":
            return self._disc_signals(win, bad, missing)
        r = lambda rr: win[rr[0] - self.row0:rr[1] - self.row0].mean(0)
        stripe = r(w["stripe_rows"]) - 0.5 * (r(w["stripe_above"]) + r(w["stripe_below"]))
        visible = median_filter(stripe, 2001)
        hidden = uniform_filter1d((stripe < w["hidden_frac"] * np.maximum(visible, 1.0)).astype(np.float32), 9) > 0.5
        clean = uniform_filter1d((~bad).astype(np.float32), 41) > 0.9
        self.stripe_level = 0.98 * getattr(self, "stripe_level", float(np.median(visible))) + 0.02 * float(np.median(visible))
        dusty = (visible < w["min_contrast_frac"] * self.stripe_level) | (
            uniform_filter1d(hidden.astype(np.float32), w["hidden_window"]) > w["max_hidden_frac"])
        readable = clean & ~missing & ~dusty
        s0, s1 = w["strip_rows"]
        strip = np.clip(win[s0 - self.row0:s1 - self.row0], 0, 255).astype(np.uint8)
        return hidden, readable, strip

    def _disc_signals(self, win, bad, missing):
        """Camera 3 / new Camera 1: a wheel is a grey disc with a hub (calibration repo cam4/wheeldisc.py).
        The bundle's wheel template is matched along the wheel band; each wheel found marks its own width
        as "wheel here", which the wheel tracker turns into one wheel (the same tracker and rules as CAM-4).
        The template may hold one picture per lighting (new Camera 1: night, morning, evening); the best
        match wins. A night picture alone matched day wheels at 0.14-0.17 and found none (2026-09-29)."""
        w = self.wh; T = self.b.wheel_template
        r0, r1 = w["rows"]; m, h = w["play"], w["half"]
        band = np.ascontiguousarray(win[r0 - m - self.row0:r1 + m - self.row0], np.float32)
        sc = np.full(win.shape[1], -1.0, np.float32)
        for t in (T if T.ndim == 3 else T[None]):
            r = cv2.matchTemplate(band, np.ascontiguousarray(t), cv2.TM_CCOEFF_NORMED).max(0)
            sc[h:h + len(r)] = np.maximum(sc[h:h + len(r)], r)
        from scipy.ndimage import maximum_filter1d
        peaks = np.flatnonzero((sc >= w["min_match"]) & (sc >= maximum_filter1d(sc, 2 * w["min_gap"] + 1)))
        hidden = np.zeros(win.shape[1], bool)
        for x in peaks:
            hidden[max(0, x - h):x + h] = True
        clean = uniform_filter1d((~bad).astype(np.float32), 41) > w.get("clean_frac", 0.9)
        readable = clean & ~missing
        s0, s1 = w["strip_rows"]
        strip = np.clip(win[s0 - self.row0:s1 - self.row0], 0, 255).astype(np.uint8)
        return hidden, readable, strip

    # ---- persistence (odometer/mosaic are not persisted: they rebuild in seconds) -------
    def snapshot(self):
        return {"counters": dict(self.counters)}

    def restore(self, snap):
        self.counters.update(snap.get("counters", {}))
