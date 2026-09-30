"""CAM-1 end-to-end lock check: real night video -> streaming trigger -> identity, as served.

    CLINKER_CALIB_ROOT=<calibration repo> python tools/cam1_lock_check.py [frames]

Frames are FFmpeg MJPEG -q:v 3 (the pipeline's encoder). Expected on the 2026-08-20 00:00
video: lock at ~20 min (LOOP + 300 slots), barcode score ~0.96, margin ~0.71.
"""
import os
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CAM = Path(os.environ.get("CLINKER_CALIB_ROOT", ROOT.parent))
sys.path.insert(0, str(ROOT))

from src.bundle import load_bundle  # noqa: E402
from src.identity import StreamingIdentity  # noqa: E402
from src.pipeline import StreamProcessor  # noqa: E402

VIDEO = CAM / "data" / "camera_1" / "night" / "NVR_ch1_main_20260820000000_20260820010000.mp4"


def main():
    n = int(sys.argv[1]) if len(sys.argv) > 1 else 36000
    b = load_bundle(ROOT / "model")
    sp = StreamProcessor(b)
    ident = StreamingIdentity(b.master, b.loop_slots, b.rules)
    sp.subscribe(ident.on_slot)
    p = subprocess.Popen(["ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-i", str(VIDEO),
                          "-map", "0:v:0", "-an", "-c:v", "mjpeg", "-q:v", "3", "-frames:v", str(n),
                          "-f", "image2pipe", "-"], stdout=subprocess.PIPE)
    buf, i, t0, locked_at = b"", 0, time.time(), None
    while True:
        chunk = p.stdout.read(1 << 22)
        if not chunk:
            break
        buf += chunk
        while (end := buf.find(b"\xff\xd9")) >= 0:
            sp.on_frame(buf[:end + 2], f"f{i}", i)
            buf = buf[end + 2:]
            i += 1
            if ident.locked and locked_at is None:
                locked_at = i
                print(f"LOCKED at frame {i} ({i / 25 / 60:.1f} min) score {ident.scores[-1]:.2f} "
                      f"margin {ident.margins[-1]:.2f}", flush=True)
            if i % 7500 == 0:
                print(f"{i / 1500:.0f} min: slots {sp.slot} (virtual {sp.counters['virtual_slots_total']}) "
                      f"locked {ident.locked} [{time.time() - t0:.0f} s]", flush=True)
    print("RESULT:", "locked" if ident.locked else "NOT locked", f"after {i} frames")
    return 0 if ident.locked else 1


if __name__ == "__main__":
    raise SystemExit(main())
