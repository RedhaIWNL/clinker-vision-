# clinker-vision-model

gRPC model service for clinker godet vision — contract v0.3, proto package
`clinkervision.inference.v2` (**backward-incompatible** with v1; regenerate stubs).

**Status: integrated real detector**. The service supports dense Tier-1
measurements, persistent Tier-2 godet state, replayable damage events, restart
snapshots, and the canned mock responder.

## Quickstart

```bash
python -m venv .venv && .venv/bin/activate        # Windows: .venv\Scripts\activate
pip install -r requirements.txt
python -m grpc_tools.protoc -Icontract \
    --python_out=src/gen --grpc_python_out=src/gen contract/inference_v2.proto
python -m pytest tests/ -q
python -m src.server --bind 0.0.0.0:50051 --mock
# real detector:
python -m src.server --bind 0.0.0.0:50051 --bundle-dir model
```

Docker:

```bash
docker build -t clinker-vision-model:rel-1 .
docker run --rm -p 50051:50051 clinker-vision-model:rel-1
```

Mock image for pipeline integration: run the same image with
`python -m src.server --bind 0.0.0.0:50051 --mock` (see
`examples/mock-usage.md`).

## Operations

```bash
# real detector (bundle mounted or baked under model/)
docker run --rm -p 50051:50051 --cpus=2 --memory=2g \
  -v $PWD/model:/app/model:ro -v $PWD/model-state:/app/state \
  -e MODEL_BUILD=rel-1 clinker-vision-model:rel-1
# mock responder (integration testing, no bundle needed):
docker run --rm -p 50051:50051 clinker-vision-model:rel-1 \
  python -m src.server --bind 0.0.0.0:50051 --mock
# fully offline proof (no network at runtime):
docker run --rm --network none clinker-vision-model:rel-1 \
  python -m src.server --bind 0.0.0.0:50051 --mock
```

Flags: `--bundle-dir` (real mode) · `--store-path` (default `state/store.db`) ·
`--max-jpeg-bytes` (default 8M) · `--frame-deadline-s` (default 5.0) ·
`--shutdown-grace-s` (default 5.0) · `--log-level` · `MODEL_BUILD` env (baked
into `model_version`).

Policy summary: backpressure, never drop in-model (TCP flow control paces the
sender; the reorder window is 64 frames); overload therefore degrades as sender
backpressure. Transport failures are reconnectable by the pipeline. Slow frames
past the deadline are counted (`deadline_exceeded_total`) and skipped, never
retried.
SIGTERM/SIGINT persist a final snapshot before draining (`shutdown-grace-s`).

Resources (measured, single CAM-1 dense stream): match+measure sustains
~1200 fps single-threaded (determinism-forced `setNumThreads(1)`); JPEG decode
of 8 MP frames is the budget to watch (bench before sign-off); RSS is flat
(rolling buffers only — no hour memmap). Start with `--cpus=2 --memory=2g` and
confirm with the soak bench.

## Bench (dev machine, 2026-09-20 — re-record on release hardware)

`tests/test_bench.py` gates, on real night frames: JPEG encode 86/s, decode
83/s, Tier-1 (decode+measure) p50 12.9 ms / p99 15.7 ms / max 16.0 ms
(~65 fps sustained vs the 25 fps budget). Soak verdict rule (plan §0): 1-hour
25 fps run passes iff Tier-1 p99 < 80 ms sustained, lag non-growing, RSS flat,
zero unaccounted gaps; otherwise the ROI-crop carve-out triggers.

## Licenses

Service code: **TBD by CILAS** (no LICENSE file yet — default all rights
reserved until one is added; required before handoff). Dependencies (pinned in
`requirements.txt`): opencv-python-headless 5.0.0.93 (Apache-2.0), numpy 2.2.6
(BSD-3), scipy 1.15.3 (BSD-3), pandas 2.3.3 (BSD-3), grpcio/grpcio-tools/
grpcio-health-checking 1.84.0 (Apache-2.0), protobuf 7.36.2 (BSD-3),
pytest 9.1.1 (MIT, test-only). Calibration bundle: derived from CILAS footage,
same TBD license as the service code.

## Behavior table (MVP)

| Item | Value |
|---|---|
| `fault_type` → meaning | `DAMAGE` = short/bent wing lip (fixed-frame measurement). No distinct `CRACK`/`MISALIGNMENT` classes exist — never faked. |
| `observation_target` → meaning | `GODET` = side-wing lips in the CAM-1 ROI. |
| Confidence | Absent until a mapping is signed. Thresholds are row counts, pipeline-side. |
| `model_version` | `<template-sha8>+<MODEL_BUILD>+<thresholds-id>` (mock: `mock-0`), on every response. |
| Input | Dense 25 fps JPEG, 2688×1520; pipeline encoder is FFmpeg MJPEG `-q:v 3`, maximum 8 MB. |
| Cameras | `CAM-1` only; anything else → `INVALID_ARGUMENT`. |
| Empty | `detections: []` = nothing qualifying = normal, no alert, nothing stored. |
| Per-frame errors | Bad JPEG / unknown camera / oversized → dead-letter (log + counter), **no response for that frame**, stream stays up. |

## Layout

`contract/` proto source · `src/` server + mock + pipeline/identity/alerts/bundle/store ·
`src/gen/` generated stubs (never hand-edit) · `model/` calibration bundle (Phase 4) ·
`tests/` gates · `examples/` artifacts · `docs/` sign-offs + runbook + compatibility statement.
