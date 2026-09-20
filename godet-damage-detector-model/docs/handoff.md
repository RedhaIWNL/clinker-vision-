# Handoff — clinker-vision-model:final

| Field | Value |
|---|---|
| Image name | `clinker-vision-model` |
| Image tag | `:rel-1` (service) — same image with `--mock` serves the canned responder for integration |
| Dockerfile path | `Dockerfile` (repo root) |
| gRPC address | `0.0.0.0:50051` (`Infer` bidi-stream, `GetGodetState`, standard health) |
| Model version | `498184ea+dev+thr-v1` (`<template-sha8>+<build>+<thresholds-id>`; set `MODEL_BUILD` per release) |
| Weights checksum | No trained weights. Calibration bundle (`model/`): `template.png 498184ea02c26714…`, `barcode.npy 185f61094412a476…`, `calibration.json 910a3669f7f8822f…` (full SHA256 in `model/CHECKSUMS.sha256`) |
| Input format | Dense 25 fps full-frame JPEG, 2688×1520, `CAM-1`, contiguous `sequence_no` |
| Output format | Tier-1 scalars (`lit_L, lit_R, peak, dx, dy, slot, status_code`) + fixed ROI box indicator; Tier-2 godet states, replayable damage events, and health |
| Recommended threshold | Row counts, pipeline-side: short below L123/R112 rows; NEW-drop ≥25 rows both views + next-loop confirmation; splay rise ≥1.0 px |
| CPU/RAM requirements | Start `--cpus=2 --memory=2g` per CAM-1 stream (CPU-only; rolling buffers, flat RSS) |
| Expected latency | Tier-1 p50 ~13 ms / p99 ~16 ms (dev machine; re-bench on release hardware); confirmed alerts one loop (~16 min) |
| Example run command | `docker run --rm -p 50051:50051 -v $PWD/model:/app/model:ro -e MODEL_BUILD=rel-1 clinker-vision-model:final` |
| Example request/response | `examples/requests/sample-request.json`, `examples/responses/*.json`, `examples/mock-usage.md` |
| Known limitations | See `docs/compatibility-statement.md` §7 |

Integration order: mock first (deterministic, no bundle), then real; drills
(retry, dead-letter, stop/resync, restart, hold-out eval) are joint work.
