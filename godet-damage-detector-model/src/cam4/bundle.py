"""Side-plate calibration bundle (CAM-4, CAM-3): load, verify checksums, model_version."""
from __future__ import annotations

import hashlib
import json
import math
import os
from dataclasses import dataclass
from pathlib import Path

import numpy as np

from src.bundle import BundleError

BUNDLE_FORMAT = "cam4-1"


@dataclass
class Cam4Bundle:
    frame_size: tuple
    p0: np.ndarray
    u: np.ndarray            # along travel (plates move +u in the image)
    v: np.ndarray            # across the plate band, toward the rail
    slit_half: int
    pad: int
    odo_a: tuple
    odo_b: tuple
    slit_a: tuple
    baselines: tuple
    det: dict
    ident: dict
    rules: dict
    chainmap: np.ndarray     # 2 x loop_cols float32, unit-std fingerprint of one loop
    thresholds_id: str
    camera_id: str           # CAM-4 | CAM-3 (from calibration.json)
    version: str             # <chainmap-sha8>+<build>+<thresholds-id>
    wheels: dict | None = None   # wheel (galet) detector settings; None = no wheel tracking


def load_cam4_bundle(d) -> Cam4Bundle:
    d = Path(d)
    for f in ("calibration.json", "chainmap.npy", "CHECKSUMS.sha256"):
        if not (d / f).exists():
            raise BundleError(f"side-plate bundle {d} missing {f}: refuse to serve")
    want = {}
    for line in (d / "CHECKSUMS.sha256").read_text().splitlines():
        h, _, name = line.partition("  ")
        if name.strip():
            want[name.strip()] = h.strip()
    for name in ("calibration.json", "chainmap.npy"):
        p = d / name
        if want.get(name) != hashlib.sha256(p.read_bytes()).hexdigest():
            raise BundleError(f"side-plate bundle checksum mismatch on {name}: refuse to serve")
    cal = json.loads((d / "calibration.json").read_text())
    if cal.get("bundle_format") != BUNDLE_FORMAT:
        raise BundleError(f"side-plate bundle format {cal.get('bundle_format')!r}, expected {BUNDLE_FORMAT!r}")
    chain = np.load(d / "chainmap.npy").astype(np.float32)
    idn = cal["identity"]
    if chain.shape != (2, idn["loop_cols"]):
        raise BundleError(f"CAM-4 chain map shape {chain.shape} != (2, {idn['loop_cols']})")
    if abs(idn["loop_cols"] / idn["godets"] - idn["pitch"]) > 1e-3:
        raise BundleError("side-plate bundle loop_cols / godets != pitch")
    g = cal["geometry"]
    ang = math.radians(g["travel_deg"])
    u = np.array([math.cos(ang), math.sin(ang)])
    v = np.array([-u[1], u[0]]) if -u[1] > 0 else np.array([u[1], -u[0]])
    sha8 = hashlib.sha256((d / "chainmap.npy").read_bytes()).hexdigest()[:8]
    build = os.environ.get("MODEL_BUILD", "dev")
    return Cam4Bundle(
        frame_size=tuple(cal["frame_size"]), p0=np.array(g["p0"], float), u=u, v=v,
        slit_half=int(g["slit_half"]), pad=int(g["mosaic_pad"]),
        odo_a=tuple(g["odo_a"]), odo_b=tuple(g["odo_b"]), slit_a=tuple(g["slit_a"]),
        baselines=tuple(g["baselines"]), det=cal["detector"], ident=idn, rules=cal["rules"],
        chainmap=chain, thresholds_id=cal["thresholds_id"], camera_id=cal.get("camera_id", "CAM-4"), wheels=cal.get("wheels"),
        version=f"{sha8}+{build}+{cal['thresholds_id']}")
