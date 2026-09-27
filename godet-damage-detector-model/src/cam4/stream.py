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
"""
from __future__ import annotations

from collections import deque

import cv2
import numpy as np
from scipy.ndimage import median_filter, uniform_filter1d

ROW0, ROW1 = 40, 280          # mosaic rows kept (detector rows 40..280, texture rows 60..280)
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
    t0 = det["texture_rows"][0] - ROW0
    texture = np.abs(cv2.Sobel(g[t0:], cv2.CV_32F, 1, 0)).mean(0)
    return top, wedge, texture


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
    fn(cols, fp_raw (2 x n), severity (n), top (n), missing (n bool))."""

    def __init__(self, bundle):
        self.b = bundle
        a0, a1 = bundle.odo_a; b0, b1 = bundle.odo_b
        self.ox, self.oy = _grid(bundle, np.arange(a0, a1), np.arange(b0, b1))
        self.win = cv2.createHanningWindow((self.ox.shape[1], self.ox.shape[0]), cv2.CV_32F)
        self.A = np.arange(*bundle.slit_a)
        rows_b = np.arange(ROW0, ROW1) - bundle.slit_half
        self.sx, self.sy = _grid(bundle, self.A, rows_b)       # (rows, len(A))
        self.wts = (1 - np.abs(self.A) / 7.0).astype(np.float32)
        H = ROW1 - ROW0
        self.acc = np.zeros((H, RING), np.float32)
        self.cnt = np.zeros(RING, np.float32)
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
        self.done = CTX                # next column to score
        self.sinks = []
        self.counters = {"frames_total": 0, "columns_total": 0, "stopped_frames_total": 0,
                         "weak_frames_total": 0, "gap_frames_total": 0}

    def subscribe(self, fn):
        self.sinks.append(fn)

    # ---- per frame -------------------------------------------------------------------
    def on_frame(self, jpeg: bytes, frame_id=None, sequence_no=None):
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
        self._paste(g, s_new)
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

    def chain_status(self):
        """0 moving, 1 weak view (low match quality), 2 stopped."""
        if len(self.recent_steps) >= 25 and float(np.median(self.recent_steps)) < 1.0:
            return 2
        if len(self.quals) >= 25 and float(np.median(list(self.quals)[-25:])) < 0.25:
            return 1
        return 0

    # ---- unrolled chain ---------------------------------------------------------------
    def _paste(self, g, s):
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

    def _finalise(self, upto):
        if upto <= self.next_final:
            return
        upto = min(upto, self.next_final + RING)
        idx = np.arange(self.next_final, upto) % RING
        cnt = self.cnt[idx]
        cols = self.acc[:, idx] / np.maximum(cnt, 1e-6)[None]
        missing = cnt == 0
        self.acc[:, idx] = 0; self.cnt[idx] = 0
        self.fin = np.concatenate([self.fin, cols], 1)
        self.fin_missing = np.concatenate([self.fin_missing, missing])
        self.counters["columns_total"] += upto - self.next_final
        self.next_final = upto
        self._score_blocks()

    def _score_blocks(self):
        while self.next_final - self.done >= CTX + BLOCK_COLS:
            a = self.done - CTX                              # window start (absolute column)
            end = self.next_final
            win = self.fin[:, a - self.fin0:end - self.fin0]
            top, wedge, texture = column_signals(win, self.b.det)
            sev = scores(top, wedge, self.b.det)
            fp = np.stack([top, texture]).astype(np.float32)
            fp = fp - cv2.blur(fp, (301, 1))
            e = end - CTX                                    # emit [done, e)
            sl = slice(self.done - a, e - a)
            miss = self.fin_missing[self.done - self.fin0:e - self.fin0]
            cols = np.arange(self.done, e)
            for fn in self.sinks:
                fn(cols, fp[:, sl], sev[sl], top[sl], miss)
            self.done = e
            keep_from = self.done - CTX                      # trim what no window needs again
            if keep_from > self.fin0:
                self.fin = self.fin[:, keep_from - self.fin0:]
                self.fin_missing = self.fin_missing[keep_from - self.fin0:]
                self.fin0 = keep_from

    # ---- persistence (odometer/mosaic are not persisted: they rebuild in seconds) -------
    def snapshot(self):
        return {"counters": dict(self.counters)}

    def restore(self, snap):
        self.counters.update(snap.get("counters", {}))
