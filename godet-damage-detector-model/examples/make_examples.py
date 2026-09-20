"""Generate committed sample artifacts from real fixtures (run once, outputs
reviewed then committed; tests/test_examples.py keeps them honest).

Writes: examples/frames/{healthy,damage,plate}.jpg (full-frame JPEG q95),
examples/responses/tier1-{healthy,damage,plate}.json (Tier-1 dicts + fixed box
where indicated), examples/responses/pending-224.json (real pending event),
examples/responses/health.json (real Tier-2 health shape).

Run:  python examples/make_examples.py   (service venv, from repo root)
"""
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tests"))
sys.path.insert(0, str(ROOT))

from support import CAM, ROOT as R, encode, read_video_frames  # noqa: E402
from test_fixtures import CASES, run_case  # noqa: E402

FR = ROOT / "examples" / "frames"
RS = ROOT / "examples" / "responses"
FR.mkdir(parents=True, exist_ok=True)
RS.mkdir(parents=True, exist_ok=True)

FIXED_BOX = {"x": 0.3806, "y": 0.1158, "width": 0.0536, "height": 0.1697}

for name, (target, half, lead) in CASES.items():
    frames = read_video_frames(target, 1)
    (FR / f"{name}.jpg").write_bytes(encode(frames, "jpg", 95)[0])
    d = run_case(name, "jpg")
    tier1 = {"frame_id": f"example-{name}", "frame": target, "model_version": "498184ea+dev+thr-v1",
             "detections": [], "scalar_measurements": {k: d[k] for k in
                ("lit_L", "lit_R", "peak", "dx", "dy", "slot", "status_code")},
             "status": d["status"]}
    if d["indicator"]:
        tier1["detections"] = [{"observation_target": "GODET", "fault_type": "DAMAGE",
                                "bounding_box": FIXED_BOX}]
    (RS / f"tier1-{name}.json").write_text(json.dumps(tier1, indent=2))
    print("wrote", name, d["status"], (d["lit_L"], d["lit_R"]))

# Tier-2 samples from a CSV-fed stack (fast, no video)
from src.alerts import AlertTracker  # noqa: E402
from src.bundle import load_bundle  # noqa: E402
from src.identity import StreamingIdentity  # noqa: E402
from support import slots_from_captures  # noqa: E402

bundle = load_bundle(ROOT / "model")
ident = StreamingIdentity(bundle.master, bundle.loop_slots, bundle.rules)
alerts = AlertTracker(bundle.rules, bundle.lines)
ident.subscribe(alerts.on_godet_row)
ident.subscribe_loops(alerts.on_loop_start)
for rec in slots_from_captures(CAM / "out" / "h00" / "captures.csv"):
    ident.on_slot(rec)
ident.flush()
alerts.flush()

ev224 = [e for e in alerts.events if e[1] == 224][0]
(RS / "pending-224.json").write_text(json.dumps(
    {"kind": ev224[0], "godet_id": ev224[1], "loop": ev224[2], "state": ev224[3],
     "payload": {k: (None if v != v else v) for k, v in ev224[4].items()
                 if k in ("lip_before", "lip_now", "drop", "status")},
     "model_version": bundle.version}, indent=2))
(RS / "health.json").write_text(json.dumps(
    {"model_version": bundle.version, "ready": ident.locked,
     "loop_locked": ident.locked, "status": "ok" if not alerts.health_lines else "see detail",
     "detail": "; ".join(alerts.health_lines) or "all clear",
     "persistent_godets": len(alerts.persistent),
     "pending": sum(1 for e in alerts.events if e[3] == "pending"),
     "confirmed": sum(1 for e in alerts.events if e[3] == "confirmed")}, indent=2))
print("wrote pending-224.json health.json")
