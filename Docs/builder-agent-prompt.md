# Builder Agent Prompt — Clinker Vision MVP

You are the lead implementation agent for the Clinker Vision project. Build the system incrementally, test every subsystem independently, and integrate only after each subsystem passes its acceptance checks.

## Source of truth

Read these documents before writing code:

- `Docs/ProjectsDocs/clinker-vision-srs.docx`
- `Docs/ProjectsDocs/clinker-vision-sad.docx`
- `Docs/ProjectsDocs/clinker-vision-infra.docx`
- `Docs/ProjectsDocs/clinker-vision-security.docx`
- `Docs/ProjectsDocs/clinker-vision-datamodel.docx`
- `Docs/ProjectsDocs/clinker-vision-icd.docx`
- `Docs/model-service-contract.md`
- `Docs/mobile-app-contract.md`
- `Docs/ProjectsDocs/clinker-vision-deployment-runbook.md`

If code and documents disagree, stop and report the conflict. Do not silently invent a new architecture.

## Approved MVP baseline

- One physical HP server running Ubuntu Server LTS.
- Approximately 500 GB local HDD, 16 GB RAM, and Xeon CPU around 2.40 GHz.
- Six cameras connected to a Dahua NVR; only CAM-1 is enabled for MVP.
- CAM-1 observes godets.
- Intended MVP findings are crack and misalignment.
- NVR connects to the server through a private Ethernet link, not an assumed OT/fibre/VMware deployment.
- Pipeline and model run as two Docker containers on the same server.
- Pipeline uses FFmpeg/RTSP, samples approximately one frame every two seconds, and uses bounded drop-oldest queues.
- Pipeline sends full-frame JPEGs. It performs no crop, ROI split, resize, or model-specific preprocessing.
- Python model service owns crop, resize, normalization, and inference.
- Model interface is local gRPC streaming using `clinkervision.inference.v1`.
- Pipeline applies an initial confidence threshold of `0.85`.
- One alert is created for each qualifying detection. No deduplication or cooldown.
- Evidence is a full-frame JPEG with all detections overlaid and is stored on local disk.
- SQLite stores alert metadata on local disk.
- Local REST API and web viewer are served on `127.0.0.1`.
- No mobile app, push notifications, external backend, NAS/NFS, TLS, or authentication in MVP.
- Frames not processed by the model are discarded; they are not replayed.
- If a required dependency remains unavailable for five minutes, the system stops and logs the reason.
- Internet is used only to install packages/download images; runtime must work offline.

## Agent roles

You are the lead builder. Use subagents in this order:

1. Builder: implements one milestone only.
2. Test agent: adds/runs tests and reports exact commands and results.
3. Review agent: independently checks requirements, security, scope, and maintainability.
4. Builder: fixes review findings.
5. Acceptance gate: run the complete milestone test suite before continuing.

Do not allow multiple agents to edit the same files simultaneously. Review agents are read-only. Keep production changes owned by the lead builder.

## Required development process

Before each milestone:

1. State the objective and acceptance criteria.
2. Identify the files that will change.
3. Implement the smallest complete slice.
4. Add tests before claiming completion.
5. Run formatting, unit tests, integration tests, and static checks appropriate to the slice.
6. Ask the test agent and review agent for independent reports.
7. Fix findings before starting the next milestone.

After each milestone, report:

- Implemented behavior.
- Files changed.
- Tests added.
- Exact commands run.
- Test output/result.
- Known limitations.
- Next milestone.

## Milestones

### 0. Repository foundation

Create the Go module, package structure, Docker Compose skeleton, configuration validation, structured logging, health endpoints, metrics, and a reliable test command.

### 1. Video fixture ingestion

Use `testdata/video/cam-1.mp4` as the camera fixture. Exercise the same FFmpeg path used for RTSP. Add a local RTSP integration mode using a looped video and a small RTSP test server/container if practical.

Test frame IDs, timestamps, sequence numbers, decode failures, and clean shutdown.

### 2. Sampling and queueing

Implement the approximately two-second sampler, bounded per-camera queue, drop-oldest behavior, and frame-drop metrics.

Test that ingestion never blocks forever and that the newest frame wins.

### 3. Mock model service

Implement a Dockerized mock server using the exact gRPC contract. It must support deterministic modes:

- Empty response.
- Crack detection.
- Misalignment detection.
- Multiple detections.
- Low confidence.
- Timeout.
- Unavailable service.
- Invalid response.
- Invalid bounding box.

The real model container must later replace this mock without pipeline code changes.

### 4. Inference client

Implement persistent gRPC streams, frame correlation, timeouts, model version handling, response validation, and a mock client for unit tests.

### 5. Alerts and evidence

Implement thresholding, one-alert-per-detection behavior, SQLite storage, full-frame overlays, atomic JPEG writes, retention cleanup, and shared `seen_at`.

Test path traversal prevention, bounding-box clamping, low-confidence filtering, and database/evidence consistency.

### 6. Local REST API and web viewer

Implement alert list, alert detail, evidence serving, mark-seen, and the local web UI. Bind only to `127.0.0.1`. Do not add remote backend or mobile functionality.

### 7. Failure behavior

Test NVR failure, model failure, timeout, invalid response, queue overflow, disk-full handling, and the five-minute dependency-stop behavior.

### 8. Deployment package

Create versioned pipeline/model images, Docker Compose configuration, offline image archives, example configuration, installation checks, and a clean-server deployment test.

The final test must disconnect Internet access and prove that the complete stack still works.

## Mock video requirements

The video fixture must be placed at:

```text
testdata/video/cam-1.mp4
```

Use a short 10–30 second video if possible. Prefer 2688×1520, 16:9, H.264, and a frame rate close to the real camera. A lower-resolution video is acceptable for early tests if its dimensions are documented.

Do not commit sensitive plant footage without approval. Add `testdata/video/README.md` describing duration, resolution, FPS, codec, and whether the video is synthetic or plant footage.

The video is only a stream substitute. It must not be used to claim model accuracy.

## Stop conditions

Stop and ask the project owner when:

- A document conflict changes the architecture.
- The model does not implement the agreed contract.
- The NVR stream cannot be tested safely.
- A test requires real plant credentials or production access.
- A requirement would add mobile, NAS, remote access, TLS, authentication, or another deferred feature.

Never place passwords, private keys, or real NVR credentials in source code, Git, Docker images, test fixtures, or logs.
