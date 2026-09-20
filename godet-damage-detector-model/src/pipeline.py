"""Streaming port of godet3 steps 1-4 (decode/template/trigger/measure).

Framework-free: on_frame() takes bytes + metadata, returns a plain Tier-1 dict (or
None when the frame is held/late and produces no response yet). The gRPC layer
(src/server.py) maps dicts to protobuf. No cam imports: lip/face measurement is
vendored verbatim from godet3/measure.py + common.py (shift, clahe).

Documented streaming deviations from the batch pipeline:
1. Plate rule uses a TRAILING median (last <=401 capture ROI means, min 50) where
   batch uses a centered rolling median. Early captures (<50) never flag plate.
   Replay gate allows <0.5% status disagreement, all near plates.
2. `edge` marking is two-sided in batch (±2 slots of a plate). Streaming marks
   edge-after immediately; edge-before is applied RETROACTIVELY to the slot table
   when a plate is found (the Tier-1 response already sent keeps its preliminary
   status). Tier-2 / slot records are authoritative, Tier-1 is preliminary.
3. Capture phase-lock, ramp rule, virtual slots and thresholds are identical.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
import time

import cv2
import numpy as np


def _f(v):
    return float(v)

# Determinism over speed: matchTemplate's parallel reduction can break
# near-ties differently run to run (observed: same pixels, dx -1 vs 0 across
# processes), sliding a capture ±1 frame. The ROI is 144x258 — single-threaded
# match is still >1000 fps (measured 1176+). Set once at import; the server
# process is dedicated to this pipeline.
try:
    cv2.setNumThreads(1)
except Exception:
    pass

# status_code enum (contract-pinned).
READABLE, WEAK, EDGE, OCCLUDED = 0, 1, 2, 3
STATUS_NAME = {READABLE: "readable", WEAK: "weak", EDGE: "edge", OCCLUDED: "occluded"}

# Fixed ROI indicator (2688x1520 CAM-1 night ROI). Identical on every indicator.
FIXED_BOX = {"x": 0.3806, "y": 0.1158, "width": 0.0536, "height": 0.1697}


def _clahe(g):
    return cv2.createCLAHE(3.0, (8, 8)).apply(np.ascontiguousarray(g))


def _shift(img, dx, dy):
    M = np.float32([[1, 0, -dx], [0, 1, -dy]])
    return cv2.warpAffine(img, M, (img.shape[1], img.shape[0]),
                          borderMode=cv2.BORDER_REPLICATE)


def _line_x(line, y):
    return line[0] * np.asarray(y, float) + line[1]


def lip_stats(A, line, thr, lit_rows, half=5, slope_half=8):
    """Verbatim port of godet3/measure.py::lip_stats."""
    y0, y1 = lit_rows
    lit, xs, vals, ax, ay = [], [], [], [], []
    for y in range(y0, y1):
        x = int(round(line[0] * y + line[1]))
        a, b = max(x - half, 0), min(x + half + 1, A.shape[1])
        seg = A[y, a:b]
        v = float(seg.max())
        if v > thr:
            lit.append(y); xs.append(a + int(np.argmax(seg)) - x); vals.append(v)
        a2, b2 = max(x - slope_half, 0), min(x + slope_half + 1, A.shape[1])
        seg2 = A[y, a2:b2]
        ax.append(a2 + int(np.argmax(seg2))); ay.append(y)
    if not lit:
        return {"lit": 0, "top": -1, "bot": -1, "bright": 0.0, "straight": np.nan, "slope": np.nan}
    slope = float(np.polyfit(ay, ax, 1)[0]) if len(ax) >= 40 else np.nan
    return {"lit": len(lit), "top": lit[0], "bot": lit[-1], "bright": float(np.mean(vals)),
            "straight": float(np.sqrt(np.mean(np.square(xs)))), "slope": slope}


def face_stats(A, T, info):
    """Verbatim port of godet3/measure.py::face_stats (info = lines + face_band)."""
    fb = info["face_band"]; L, Rl = info["lines"]["L"], info["lines"]["R"]
    ys = np.arange(fb["y0"], fb["y1"])
    xm = ((_line_x(L, ys) + _line_x(Rl, ys)) / 2).round().astype(int)
    diff, vals = [], []
    for y, x in zip(ys, xm):
        a, b = x - fb["half_w"], x + fb["half_w"]
        diff.append(np.abs(A[y, a:b] - T[y, a:b])); vals.append(A[y, a:b])
    return float(np.mean(np.concatenate(diff))), float(np.mean(np.concatenate(vals)))


@dataclass
class SlotRecord:
    slot: int
    kind: str            # real | virtual
    frame_id: object = None
    sequence_no: object = None
    dx: float = np.nan
    dy: float = np.nan
    peak: float = np.nan
    status: str = "occluded"
    lit_L: float = np.nan
    lit_R: float = np.nan
    short_L: bool = False
    short_R: bool = False
    # full per-capture stats for Tier-2 (fill/splay/tilt/face rules). NaN unless real.
    top_L: float = np.nan
    bot_L: float = np.nan
    bright_L: float = np.nan
    straight_L: float = np.nan
    slope_L: float = np.nan
    top_R: float = np.nan
    bot_R: float = np.nan
    bright_R: float = np.nan
    straight_R: float = np.nan
    slope_R: float = np.nan
    face_resid: float = np.nan
    t_wall: float = np.nan  # arrival wall-clock (health rate rules; measurement is slot-based)

    def to_dict(self):
        import dataclasses
        import math

        out = {}
        for f in dataclasses.fields(self):
            v = getattr(self, f.name)
            out[f.name] = "__nan__" if isinstance(v, float) and math.isnan(v) else v
        for k in ("row_done", "gid_assigned", "loop_assigned"):
            if hasattr(self, k):
                out[k] = getattr(self, k)
        return out

    @classmethod
    def from_dict(cls, d):
        import dataclasses

        names = {f.name for f in dataclasses.fields(cls)}
        kw = {k: (float("nan") if v == "__nan__" else v) for k, v in d.items() if k in names}
        rec = cls(**kw)
        for k in ("row_done", "gid_assigned", "loop_assigned"):
            if k in d:
                setattr(rec, k, d[k])
        return rec


class StreamProcessor:
    """One instance per dense stream (CAM-1). Single-threaded: call on_frame()
    strictly in the order frames should be sequenced (the server holds the
    reorder window and only releases in-order frames here)."""

    def __init__(self, bundle):
        self.b = bundle
        r0, r1 = bundle.core_rows; c0, c1 = bundle.core_cols
        self.core = bundle.T[r0:r1, c0:c1].astype(np.float32)
        self.core_off = (c0, r0)
        # dx/dy/peak ring over accepted frames: lists of (pos, dx, dy, peak).
        self.hist = deque(maxlen=64)
        self.pos = 0
        self.last_cap_pos = -10 ** 9
        self.slot = 0
        self.roi_hist = deque(maxlen=401)   # trailing ROI means of captures
        self.frame_peaks = deque(maxlen=200)  # per-frame match peaks (live
        # template check pre-lock: nothing godet-like in view for 100+ frames)
        self._prev = None                   # previous frame's crop+match (nearer-zero rule)
        self.slots: list = []               # slot table (Tier-2 source of truth)
        self.sinks = []                     # on_slot(record) callbacks (Tier-2 streamer)
        self.counters = {"frames_total": 0, "captures_total": 0,
                         "virtual_slots_total": 0}

    def subscribe(self, fn):
        """Tier-2 attaches here; every slot record (real + virtual) is emitted
        in slot order, exactly once."""
        self.sinks.append(fn)

    def _emit(self, rec):
        self.slots.append(rec)
        for fn in self.sinks:
            fn(rec)

    # ---- low level ------------------------------------------------------
    def _match(self, roi):
        r = cv2.matchTemplate(_clahe(roi).astype(np.float32), self.core,
                              cv2.TM_CCOEFF_NORMED)
        _, mx, _, loc = cv2.minMaxLoc(r)
        dx = loc[0] - self.core_off[0]
        dy = loc[1] - self.core_off[1]
        return dx, dy, float(mx)

    def _crossing(self):
        """Zero crossing with a clean 4-step ramp, like
        godet3/trigger.py::captures_from_phase. Returns the capture frame's
        offset from the current frame (0 or -1: batch keeps the frame nearer to
        zero), or None."""
        h = list(self.hist)
        if len(h) < 6:
            return None
        dx = np.array([x[1] for x in h], float)
        i = len(dx) - 1
        if not (dx[i - 1] < 0 <= dx[i]):
            return None
        steps = dx[i - 4 + 1:i + 1] - dx[i - 4:i]
        if not np.all((steps > 0) & (steps < 9)):
            return None
        return 0 if abs(dx[i]) <= abs(dx[i - 1]) else -1

    # ---- public ---------------------------------------------------------
    def on_roi(self, roi, frame_id=None, sequence_no=None):
        """Process one ROI crop (grey uint8 HxW) in stream order. Returns a Tier-1
        dict for EVERY frame; non-capture frames carry match scalars + slot -1."""
        g = np.ascontiguousarray(roi)
        dx, dy, peak = self._match(g)
        self.frame_peaks.append(float(peak))
        self.hist.append((self.pos, dx, dy, peak))
        self.counters["frames_total"] += 1
        out = {"frame_id": frame_id, "is_capture": False, "peak": peak,
               "dx": float(dx), "dy": float(dy), "slot": -1}
        off = self._crossing()
        if off is not None:
            if off == 0:
                crop, mdx, mdy, mpeak, fpos = g, dx, dy, peak, self.pos
            else:  # nearer-to-zero frame is the previous one: measure it
                prev = self._prev
                crop, mdx, mdy, mpeak, fpos = (prev["roi"], prev["dx"],
                                               prev["dy"], prev["peak"], self.pos - 1)
            if fpos - self.last_cap_pos > 12:  # min_sep, as batch
                rec = self._measure(crop, mdx, mdy, mpeak, fpos,
                                    frame_id, sequence_no)
                out.update(rec)
                if off == -1:  # scalars describe the capture frame, as batch
                    out.update({"peak": mpeak, "dx": float(mdx), "dy": float(mdy)})
        self._prev = {"roi": np.ascontiguousarray(g).copy(),
                      "dx": dx, "dy": dy, "peak": peak}
        self.pos += 1
        return out

    def on_frame(self, jpeg: bytes, frame_id=None, sequence_no=None):
        """Full-frame JPEG -> ROI crop -> on_roi. Returns (tier1_dict, crops_ok).
        crops_ok False means the JPEG decoded but the ROI fell outside the frame
        (mis-sized image): counted by the caller as a dead letter."""
        img = cv2.imdecode(np.frombuffer(jpeg, dtype=np.uint8), cv2.IMREAD_GRAYSCALE)
        if img is None or img.size == 0:
            return None, False
        r = self.b.roi
        if img.shape[0] < r["y"] + r["h"] or img.shape[1] < r["x"] + r["w"]:
            return None, False
        roi = img[r["y"]:r["y"] + r["h"], r["x"]:r["x"] + r["w"]]
        return self.on_roi(roi, frame_id, sequence_no), True

    def virtual_slot(self):
        """A sequence gap hides whole godets: count them so identity (Phase 3)
        stays aligned. Mirrors trigger.build_table virtual rows."""
        self._emit(SlotRecord(slot=self.slot, kind="virtual", t_wall=time.time()))
        self.slot += 1
        self.counters["virtual_slots_total"] += 1

    # ---- measurement ----------------------------------------------------
    def _measure(self, g, dx, dy, peak, fpos, frame_id, sequence_no):
        b = self.b
        roi_mean, roi_std = float(g.mean()), float(g.std())
        A = _shift(_clahe(g).astype(np.float32), int(dx), int(dy))
        info = {"lines": b.lines, "face_band": b.face_band}
        sL = lip_stats(A, b.lines["L"], b.lip_thr["L"], b.lit_rows["L"])
        sR = lip_stats(A, b.lines["R"], b.lip_thr["R"], b.lit_rows["R"])
        fr, _fm = face_stats(A, b.T, info)
        weak = (peak < b.peak_readable) or (abs(dy) >= 38) or (abs(dx) >= 30)
        med = float(np.median(self.roi_hist)) if len(self.roi_hist) >= 50 else np.nan
        plate = weak and (not np.isnan(med)) and (roi_mean > med + 6.0)
        if plate:
            status = "occluded"
        elif weak or (fr > 20.0):
            status = "weak"
        else:
            status = "readable"
        # edge-after: within 2 slots behind a plate. Edge-before is retro-marked
        # below when a plate is found (Tier-1 already sent keeps preliminary status).
        if status in ("readable", "weak") and any(
                s.status == "occluded" for s in self.slots[-2:]):
            status = "edge"
        short_L = status != "occluded" and sL["lit"] < b.short_len["L"]
        short_R = status != "occluded" and sR["lit"] < b.short_len["R"]
        rec = SlotRecord(slot=self.slot, kind="real", frame_id=frame_id,
                         sequence_no=sequence_no, dx=float(dx), dy=float(dy),
                         peak=float(peak), status=status,
                         lit_L=float(sL["lit"]), lit_R=float(sR["lit"]),
                         short_L=bool(short_L), short_R=bool(short_R),
                         top_L=_f(sL["top"]), bot_L=_f(sL["bot"]),
                         bright_L=_f(sL["bright"]), straight_L=_f(sL["straight"]),
                         slope_L=_f(sL["slope"]),
                         top_R=_f(sR["top"]), bot_R=_f(sR["bot"]),
                         bright_R=_f(sR["bright"]), straight_R=_f(sR["straight"]),
                         slope_R=_f(sR["slope"]), face_resid=float(fr),
                         t_wall=time.time())
        self._emit(rec)
        if status == "occluded":
            for s in self.slots[-3:-1]:  # retro-mark ≤2 slots before the plate
                if s.kind == "real" and s.status in ("readable", "weak"):
                    s.status = "edge"
        self.roi_hist.append(roi_mean)
        self.last_cap_pos = fpos  # chosen capture frame, for min_sep
        self.slot += 1
        self.counters["captures_total"] += 1
        indicator = (short_L or short_R) and status != "occluded"
        return {"is_capture": True, "slot": rec.slot, "frame": fpos,
                "status_code": {"readable": 0, "weak": 1, "edge": 2,
                                "occluded": 3}[status],
                "status": status, "lit_L": float(sL["lit"]),
                "lit_R": float(sR["lit"]), "indicator": bool(indicator)}
