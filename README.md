# Clinker Vision

The current integration uses the delivered CAM-1 godet model: dense ordered
25-FPS JPEG input, v2 gRPC measurements, Tier-2 godet-state polling, SQLite
alerts, local evidence storage, and a localhost-only REST alert viewer.
Tier-2 responses include replayable damage events with stable deduplication keys;
the pipeline correlates their candidate frame IDs with a sparse evidence cache.

## Local checks

Install Go and Docker Compose, then run:

```bash
make check
```

The configuration loader requires a protected runtime file. Copy the example,
replace the RTSP placeholder with a protected value, and set mode `0600` before
starting the binary:

```bash
install -D -m 600 deploy/config.example.yaml config/config.yaml
go run ./cmd/pipeline -config config/config.yaml
```

Compose uses the same `config/config.yaml` path. The file must be created before
starting the stack; it is ignored by Git.

The foundation endpoints are:

```text
GET /health/live
GET /health/ready
GET /metrics
GET /
GET /api/v1/status
GET /api/v1/alerts
GET /api/v1/alerts/{alert_id}
GET /api/v1/alerts/{alert_id}/evidence
POST /api/v1/alerts/{alert_id}/seen
```

Alert listing supports camera, fault, unseen, since, limit, and opaque cursor
filters. The HTML viewer is intentionally local and has no authentication or
remote access features.

Compose builds the delivered model from `godet-damage-detector-model/`, mounts
its checksum-verified calibration bundle read-only, and keeps model state in
`model-state/`. The model is CPU-only and starts with a 2 CPU / 2 GiB budget.
The old Go mock remains available for isolated v1 tests, but is not the Compose
model service.

Release image tags and the v1-to-v2 contract decision are documented in
[docs/contract-adoption.md](docs/contract-adoption.md); copy `.env.example` to
`.env` when selecting a release tag.

For Ubuntu host setup, permissions, RTSP testing, startup, backup, and the
current evidence-correlation boundary, see [docs/deployment.md](docs/deployment.md).

Regenerate the Go protobuf files with:

```bash
make proto
```
