"""Calibration bundle load + verify + model_version.

Dev format 0 (assembled from cam out/h00 + config; Phase 4's godet3/bundle.py emits
format 1 with identical semantics). The service never imports cam code: every
constant it needs lives in model/calibration.json.
"""
from __future__ import annotations

import hashlib
import json
import os
from dataclasses import dataclass
from pathlib import Path


class BundleError(Exception):
    pass


@dataclass
class Bundle:
    T: object            # float32 template (H x W), CLAHE domain
    core_rows: tuple     # (start, stop) rows of the trigger core inside T
    core_cols: tuple     # (start, stop) cols
    lines: dict          # {"L": [a, b], "R": [a, b]} lip lines x = a*y + b
    lip_thr: dict
    lit_rows: dict       # {"L": [y0, y1], "R": [y0, y1]} search ranges
    short_len: dict
    normal_len: dict
    face_band: dict
    peak_readable: float
    rules: dict            # alert/identity rules (single source of truth)
    master: object         # bool array: master separator barcode (one loop)
    roi: dict
    gate: dict
    period_s: float
    loop_slots: int
    thresholds_id: str
    constants: dict
    template_sha8: str
    version: str         # <template-sha8>+<build>+<thresholds-id>


def _sha8_file(p: Path) -> str:
    return hashlib.sha256(p.read_bytes()).hexdigest()[:8]


def load_bundle(d) -> Bundle:
    d = Path(d)
    for f in ("template.png", "calibration.json"):
        if not (d / f).exists():
            raise BundleError(f"bundle {d} missing {f}: refuse to serve")
    import cv2
    import numpy as np

    T = cv2.imread(str(d / "template.png"), cv2.IMREAD_GRAYSCALE)
    if T is None or T.size == 0:
        raise BundleError(f"bundle {d}/template.png unreadable: refuse to serve")
    cal = json.loads((d / "calibration.json").read_text())
    if cal.get("bundle_format") != 1:
        raise BundleError(f"bundle {d} format {cal.get('bundle_format')}: "
                          f"rebuild with godet3/bundle.py (format 1)")

    # Checksum verify against CHECKSUMS.sha256 when present (fail closed).
    chk = d / "CHECKSUMS.sha256"
    if chk.exists():
        want = {}
        for line in chk.read_text().splitlines():
            h, _, name = line.partition("  ")
            want[name.strip()] = h.strip()
        for name, h in want.items():
            p = d / name
            if not p.exists() or hashlib.sha256(p.read_bytes()).hexdigest() != h:
                raise BundleError(f"bundle checksum mismatch on {name}: refuse to serve")

    for k in ("L", "R"):
        a = cal["lines"][k][0]
        if not 0.05 <= abs(a) <= 0.20:
            raise BundleError(f"bundle lip line {k} slope {a}: not two clean bars")

    tsha = _sha8_file(d / "template.png")
    build = os.environ.get("MODEL_BUILD", "dev")
    version = f"{tsha}+{build}+{cal['thresholds_id']}"
    if not (d / "barcode.npy").exists():
        raise BundleError(f"bundle {d} missing barcode.npy: refuse to serve")
    master = np.load(d / "barcode.npy").astype(bool)
    if len(master) != cal["loop_slots"]:
        raise BundleError(f"bundle barcode len {len(master)} != loop_slots "
                          f"{cal['loop_slots']}: refuse to serve")
    return Bundle(
        T=T.astype(np.float32),
        core_rows=tuple(cal["core"]["rows"]),
        core_cols=tuple(cal["core"]["cols"]),
        lines=cal["lines"], lip_thr=cal["lip_thr"], lit_rows=cal["lit_rows"],
        short_len=cal["short_len"], normal_len=cal["normal_len"],
        face_band=cal["face_band"], peak_readable=cal["peak_readable"],
        rules=cal["rules"],
        roi=cal["roi"], gate=cal["gate"], period_s=cal["period_s"],
        loop_slots=cal["loop_slots"], thresholds_id=cal["thresholds_id"],
        constants=cal["constants"], template_sha8=tsha, version=version,
        master=master,
    )
