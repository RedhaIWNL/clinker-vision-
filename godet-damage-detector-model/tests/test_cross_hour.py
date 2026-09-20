"""Cross-hour identity (fast, no video): h01 slot records aligned against the h00
master must reproduce batch's loop starts (offset 336) — the proof that godet
ids mean the same thing across recordings.

Run:  python -m pytest tests/test_cross_hour.py -q -s
"""
import sys
from pathlib import Path

import json

from support import CAM, ROOT, slots_from_captures

sys.path.insert(0, str(ROOT))

from src.bundle import load_bundle  # noqa: E402
from src.identity import StreamingIdentity  # noqa: E402


def test_h01_aligns_to_h00_master():
    bundle = load_bundle(ROOT / "model")
    batch = json.loads((CAM / "out" / "h01" / "identity.json").read_text())
    ident = StreamingIdentity(bundle.master, bundle.loop_slots, bundle.rules)
    for rec in slots_from_captures(CAM / "out" / "h01" / "captures.csv"):
        ident.on_slot(rec)
    assert ident.locked, "never locked on h01"
    print(f"\nstreaming starts {ident.starts} vs batch {batch['loop_starts']}")
    assert ident.starts == batch["loop_starts"], "loop starts differ from batch"
    assert ident.starts[1] == 336
    assert all(s >= 0.6 for s in ident.scores[1:] if s == s)
    assert all(m >= 0.2 for m in ident.margins[1:] if m == m)
