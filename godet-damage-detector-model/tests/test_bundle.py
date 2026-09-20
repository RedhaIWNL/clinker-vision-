"""Phase 4 gates: the builder reproduces model/ byte-identically, and the
service refuses corrupt/foreign bundles loudly (BundleError, never silent).

Run:  python -m pytest tests/test_bundle.py -q -s
"""
import hashlib
import shutil
import sys
from pathlib import Path

import numpy as np
import pytest

ROOT = Path(__file__).resolve().parents[1]
CAM = ROOT.parent
sys.path.insert(0, str(ROOT / "src" / "gen"))
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(CAM))

from src.bundle import BundleError, load_bundle  # noqa: E402


def _bytes(p):
    return Path(p).read_bytes()


def test_builder_reproduces_model(tmp_path):
    sys.path.insert(0, str(CAM))
    import os

    os.chdir(CAM)
    from godet3.bundle import build

    a = tmp_path / "a"
    b = tmp_path / "b"
    build(CAM / "out" / "h00", CAM / "config" / "camera_1_night.json", a)
    build(CAM / "out" / "h00", CAM / "config" / "camera_1_night.json", b)
    for f in ("template.png", "barcode.npy", "calibration.json", "CHECKSUMS.sha256"):
        assert _bytes(a / f) == _bytes(b / f), f"builder not deterministic: {f}"
        assert _bytes(a / f) == _bytes(ROOT / "model" / f), f"model/ drifted: {f}"
    print("\nbuilder reproduces model/ byte-identically (format 1)")


def test_version_stable():
    b = load_bundle(ROOT / "model")
    assert b.version == "498184ea+dev+thr-v1", b.version
    assert b.loop_slots == 1208 and len(b.master) == 1208
    assert set(b.rules) >= {"drop_rows", "hist_loops", "barcode_min_score"}


def _corrupt(dst, name, mutate):
    shutil.copytree(ROOT / "model", dst, ignore=shutil.ignore_patterns("CHECKSUMS*"))
    (Path(dst) / "CHECKSUMS.sha256").write_text(
        (ROOT / "model" / "CHECKSUMS.sha256").read_text())
    mutate(Path(dst) / name)
    return dst


def test_missing_file_refused(tmp_path):
    d = tmp_path / "nocal"
    d.mkdir()
    with pytest.raises(BundleError):
        load_bundle(d)


def test_tampered_template_refused(tmp_path):
    import numpy as np

    def flip(p):
        import cv2

        img = cv2.imread(str(ROOT / "model" / "template.png"), cv2.IMREAD_GRAYSCALE)
        img[0, 0] ^= 0xFF
        cv2.imwrite(str(p), img)

    with pytest.raises(BundleError):
        load_bundle(_corrupt(tmp_path / "tampered", "template.png", flip))


def test_tampered_calibration_refused(tmp_path):
    import json

    def tamper(p):
        cal = json.loads(p.read_text())
        cal["short_len"]["L"] += 5
        p.write_text(json.dumps(cal, indent=2))

    with pytest.raises(BundleError):
        load_bundle(_corrupt(tmp_path / "tampered2", "calibration.json", tamper))


def test_legacy_format_refused(tmp_path):
    import json

    def downgrade(p):
        cal = json.loads(p.read_text())
        cal["bundle_format"] = 0
        p.write_text(json.dumps(cal, indent=2))

    d = _corrupt(tmp_path / "legacy", "calibration.json", downgrade)
    # re-sign so the failure is about the FORMAT, not the checksum
    lines = []
    for f in ("template.png", "barcode.npy", "calibration.json"):
        h = hashlib.sha256((Path(d) / f).read_bytes()).hexdigest()
        lines.append(f"{h}  {f}")
    (Path(d) / "CHECKSUMS.sha256").write_text("\n".join(lines) + "\n")
    with pytest.raises(BundleError, match="format"):
        load_bundle(d)


def _h00_bits():
    import pandas as pd

    from support import CAM

    df = pd.read_csv(CAM / "out" / "h00" / "captures.csv")
    valid = df["status"].isin(("readable", "weak", "edge"))
    return ((df["status"] == "occluded") | ((df["short_L"] | df["short_R"]) & valid)).values


def test_loop_drift_on_real_bits():
    from src.identity import autocorr_peak

    est, r = autocorr_peak(_h00_bits(), 900, 1500)
    print(f"\nre-estimated loop {est} (r={r:.2f}) vs pinned 1208")
    assert est == 1208, f"drift estimator broken on real bits: {est}"
    assert r >= 0.5


def test_loop_drift_alarm_path():
    from src.bundle import load_bundle
    from src.identity import StreamingIdentity

    bundle = load_bundle(ROOT / "model")
    ident = StreamingIdentity(bundle.master, bundle.loop_slots, bundle.rules)
    ident.bits = list(_h00_bits()[:3000])
    ident._check_drift()
    assert ident.loop_drift_est == 1208
    assert not ident.chain_changed

    # a foreign periodic chain must trip the alarm, never silently renumber
    n = 3 * 1208
    foreign = (np.arange(n) % 20 == 0)
    ident2 = StreamingIdentity(bundle.master, bundle.loop_slots, bundle.rules)
    ident2.bits = list(foreign)
    ident2._check_drift()
    assert ident2.chain_changed, "foreign chain did not trip drift alarm"
