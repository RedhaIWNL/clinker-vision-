"""Replay recorded CAM-4 video through the CAM-4 engine exactly as the service receives it.

Frames are encoded by FFmpeg as MJPEG -q:v 3 (the pipeline's encoder) and fed in order with
sequence numbers that restart at 1 for every video, like a new operating window. The same
engine instance runs all videos, so godet history carries over (as in production).

    python tools/cam4_replay.py --bundle model/cam4 --out <dir> video1.mp4 [video2.mp4 ...]

Writes <out>/replay.json (per-minute odometer, locks, confirmed godets, events, timing) and
<out>/evidence/<frame_id>.jpg for every frame the engine handed back as evidence.
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import re
import subprocess
import sys
import time
from pathlib import Path

import numpy as np
from google.protobuf.timestamp_pb2 import Timestamp

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src" / "gen"))
sys.path.insert(0, str(ROOT))

import inference_v2_pb2 as pb2  # noqa: E402
from src.cam4.engine import Cam4Engine  # noqa: E402

FPS = 25.0


def mjpeg_frames(video, max_frames=0):
    cmd = ["ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-i", str(video),
           "-map", "0:v:0", "-an", "-c:v", "mjpeg", "-q:v", "3"]
    if max_frames:
        cmd += ["-frames:v", str(max_frames)]
    cmd += ["-f", "image2pipe", "-"]
    p = subprocess.Popen(cmd, stdout=subprocess.PIPE, bufsize=1 << 24)
    buf = b""
    while True:
        chunk = p.stdout.read(1 << 22)
        if not chunk:
            break
        buf += chunk
        while True:
            end = buf.find(b"\xff\xd9")
            if end < 0:
                break
            yield buf[:end + 2]
            buf = buf[end + 2:]
    p.wait()


def video_start(video):
    m = re.search(r"_(\d{14})_\d{14}", Path(video).name)
    t = dt.datetime.strptime(m.group(1), "%Y%m%d%H%M%S") if m else dt.datetime(2026, 1, 1)
    return t.replace(tzinfo=dt.timezone(dt.timedelta(hours=1)))   # Africa/Casablanca


class Req:
    __slots__ = ("sequence_no", "jpeg", "frame_id", "captured")

    def __init__(self, seq, jpeg, frame_id, captured):
        self.sequence_no, self.jpeg, self.frame_id, self.captured = seq, jpeg, frame_id, captured


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bundle", default=str(ROOT / "model" / "cam4"))
    ap.add_argument("--out", required=True)
    ap.add_argument("--max-frames", type=int, default=0, help="per video (0 = all)")
    ap.add_argument("videos", nargs="+")
    a = ap.parse_args()
    out = Path(a.out); (out / "evidence").mkdir(parents=True, exist_ok=True)
    eng = Cam4Engine(a.bundle)
    report = {"bundle": eng.version, "videos": [], "minutes": []}
    kept = 0

    for vi, video in enumerate(a.videos):
        t_start = video_start(video)
        t0 = time.time(); n = 0; busy = 0.0
        minute = {"steps": [], "quals": [], "status": []}
        lock_log = []
        was_locked = eng.ident.locked

        def release(r):
            nonlocal n, busy, kept
            ts = Timestamp(); ts.FromDatetime(r.captured)
            c0 = time.perf_counter()
            d, ok = eng.on_frame(r.jpeg, r.frame_id, r.sequence_no, ts)
            for rec in eng.take_retained(n=100):
                (out / "evidence" / f"{rec['frame_id']}.jpg").write_bytes(rec["jpeg"]); kept += 1
            busy += time.perf_counter() - c0
            n += 1
            if ok:
                minute["steps"].append(d["chain_step"]); minute["quals"].append(d["match_quality"])
                minute["status"].append(d["chain_status"])

        for i, jpeg in enumerate(mjpeg_frames(video, a.max_frames)):
            seq = i + 1
            fid = f"v{vi}-{seq:07d}"
            eng.sequence(Req(seq, jpeg, fid, t_start + dt.timedelta(seconds=i / FPS)), release)
            if eng.ident.locked != was_locked:
                lock_log.append({"frame": seq, "locked": eng.ident.locked, "t_s": round(i / FPS, 1)})
                was_locked = eng.ident.locked
            if seq % int(60 * FPS) == 0:
                st = np.array(minute["status"])
                report["minutes"].append({
                    "video": vi, "minute": seq // int(60 * FPS),
                    "step_median": round(float(np.median(minute["steps"])), 2),
                    "quality_median": round(float(np.median(minute["quals"])), 3),
                    "stopped_frac": round(float(np.mean(st == 2)), 3),
                    "weak_frac": round(float(np.mean(st == 1)), 3),
                    "locked": eng.ident.locked, "passes": eng.ident.counters["passes_total"],
                    "confirmed": sum(v["state"] == "confirmed" for v in eng.hist.g.values()),
                    "ring": len(eng.ring), "retained": eng.counters["retained_frames_total"]})
                print(json.dumps(report["minutes"][-1]), flush=True)
                minute = {"steps": [], "quals": [], "status": []}
        wall = time.time() - t0
        report["videos"].append({"video": str(video), "frames": n, "wall_s": round(wall, 1),
                                 "engine_ms_per_frame": round(1000 * busy / max(n, 1), 2),
                                 "overall_fps": round(n / wall, 1), "lock_log": lock_log,
                                 "identity": dict(eng.ident.counters), "stream": dict(eng.stream.counters),
                                 "engine": dict(eng.counters)})
        print(json.dumps(report["videos"][-1]), flush=True)

    st = eng.godet_state(pb2.GodetStateRequest(include_history=True), pb2)
    report["confirmed"] = sorted(g.godet_id for g in st.godets if g.state == "confirmed")
    report["suspect"] = sorted(g.godet_id for g in st.godets if g.state == "suspect")
    report["events"] = [{"key": e.event_key, "godet": e.godet_id, "loop": e.loop_no,
                         "severity": round(e.measurements["severity"], 2),
                         "evidence_frame_id": e.evidence_frame_id,
                         "box": [round(e.evidence_box.x, 4), round(e.evidence_box.y, 4),
                                 round(e.evidence_box.width, 4), round(e.evidence_box.height, 4)]}
                        for e in st.events]
    report["health"] = {"status": st.health.status, "detail": st.health.detail,
                        "counters": dict(st.health.counters)}
    report["evidence_files"] = kept
    (out / "replay.json").write_text(json.dumps(report, indent=1))
    print("confirmed", len(report["confirmed"]), report["confirmed"])


if __name__ == "__main__":
    main()
