"""Streaming slot table vs batch, on real video with the pipeline's own encoder.

Identity aligns a barcode with one bit per SLOT (godet), so the slot table must count every
godet, including the ones the trigger could not read (virtual slots, batch rule: a capture gap
> 1.2 s hides round(gap / 0.8) - 1 godets). Before 2026-09-27 the streaming trigger emitted no
virtual slots, so the live CAM-1 barcode drifted and identity never locked. The Tier-2 tests
did not see it because they feed identity from the batch table.

Frames 0-4500 of the h00 night video (3 min; batch has 13 virtual slots there), encoded as
FFmpeg MJPEG -q:v 3 exactly like the pipeline. About 1-2 minutes.

The full lock check (30 min of video, locks at ~20 min) is tools/cam1_lock_check.py.

Run:  python -m pytest tests/test_stream_slots.py -q -s
"""
import os
import shutil
import subprocess
import sys
from pathlib import Path

import pandas as pd
import pytest

ROOT = Path(__file__).resolve().parents[1]
CAM = Path(os.environ.get("CLINKER_CALIB_ROOT", ROOT.parent))
sys.path.insert(0, str(ROOT))

from src.bundle import load_bundle  # noqa: E402
from src.pipeline import StreamProcessor  # noqa: E402

VIDEO = CAM / "data" / "camera_1" / "night" / "NVR_ch1_main_20260820000000_20260820010000.mp4"
BATCH = CAM / "out" / "h00" / "captures.csv"
COUNT = 4500

pytestmark = pytest.mark.skipif(not (VIDEO.exists() and BATCH.exists() and shutil.which("ffmpeg")),
                                reason="needs the h00 video, out/h00/captures.csv and ffmpeg")


def pipeline_jpegs(video, count):
    """Frames exactly as the pipeline sends them (ingest.Decoder: MJPEG -q:v 3)."""
    p = subprocess.Popen(["ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-i", str(video),
                          "-map", "0:v:0", "-an", "-c:v", "mjpeg", "-q:v", "3", "-frames:v", str(count),
                          "-f", "image2pipe", "-"], stdout=subprocess.PIPE)
    buf = b""
    while True:
        chunk = p.stdout.read(1 << 22)
        if not chunk:
            break
        buf += chunk
        while (end := buf.find(b"\xff\xd9")) >= 0:
            yield buf[:end + 2]
            buf = buf[end + 2:]
    p.wait()


def test_streaming_slot_table_matches_batch_including_virtual_slots():
    sp = StreamProcessor(load_bundle(ROOT / "model"))
    for i, jpeg in enumerate(pipeline_jpegs(VIDEO, COUNT)):
        sp.on_frame(jpeg, f"f{i}", i)
    real = [s for s in sp.slots if s.kind == "real"]
    assert real, "no captures: wrong video or ROI"
    last_frame = max(int(s.sequence_no) for s in real)           # sequence_no = frame index here
    batch = pd.read_csv(BATCH)
    batch = batch[batch["frame"] <= last_frame + 1]
    n_virtual = sum(s.kind == "virtual" for s in sp.slots)
    b_real, b_virtual = int((batch["kind"] == "real").sum()), int((batch["kind"] == "virtual").sum())
    print(f"\nstream: {len(real)} real + {n_virtual} virtual | batch: {b_real} real + {b_virtual} virtual")
    assert b_virtual >= 5, "window must contain missed godets to prove anything"
    assert abs(len(real) - b_real) <= 1                          # ±1 at the window edge
    assert abs(n_virtual - b_virtual) <= 1
    assert abs(len(sp.slots) - len(batch)) <= 1
