# Ubuntu deployment

This deployment runs both services on the Ubuntu host. The pipeline is reachable
on the host at `127.0.0.1:8080` (compose publishes `127.0.0.1:...:8080`, so no
LAN exposure); inside its container it listens on `0.0.0.0:8080` via
`PIPELINE_BIND_ADDRESS`, otherwise Docker NAT to the container IP gets RST.
Bare-metal `go run` keeps the config default `127.0.0.1`. The model is
reachable only inside the Compose network.

## First setup

From the project directory:

```bash
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"
newgrp docker

mkdir -p config data evidence logs model-state
cp deploy/config.example.yaml config/config.yaml
cp .env.example .env
chmod 600 config/config.yaml
sudo chown 65532:65532 data evidence logs model-state
```

Edit `config/config.yaml` and replace `REDACTED` with the NVR password. The
current CAM-1 source is:

```text
rtsp://admin:PASSWORD@192.168.1.200:554/cam/realmonitor?channel=1&subtype=0
```

If the password contains `@`, `:`, `/`, `?`, or `#`, URL-encode it before
putting it in the RTSP URL. Keep the file mode at `0600`; the container runs as
UID/GID `65532`, so the file must also be readable by that UID:

```bash
sudo chown 65532:65532 config/config.yaml
sudo chmod 600 config/config.yaml
```

Test the NVR stream before starting the stack:

```bash
ffprobe -rtsp_transport tcp -v error \
  -show_entries stream=codec_name,width,height,r_frame_rate \
  -of default=noprint_wrappers=1 \
  'rtsp://admin:PASSWORD@192.168.1.200:554/cam/realmonitor?channel=1&subtype=0'
```

Do not commit the URL with the password.

## Build and run

```bash
docker compose build
docker compose up -d
docker compose ps
docker compose logs -f --tail=100 model pipeline
```

The model container must be able to write `model-state/store.db`. If it exits
with a permission error, repair the bind-mount ownership and recreate it:

```bash
sudo chown -R 65532:65532 model-state
docker compose up -d --force-recreate model
```

The model TCP health check only means gRPC is listening. Pipeline readiness is
separate: the detector stays not-ready until its barcode alignment locks. Check
both endpoints locally:

```bash
curl -fsS http://127.0.0.1:8080/health/live
curl -sS http://127.0.0.1:8080/health/ready
curl -sS http://127.0.0.1:8080/metrics
```

Compose starts the pipeline only after the model health check passes
(`depends_on: service_healthy`), and the pipeline retries the model dial
through the configured dependency timeout, so a slow model start no longer
kills the pipeline. If the model container is recreated, the pipeline
re-resolves `model:50051` (fresh dial) instead of sticking to the stale IP.

## Daylight smoke test (temporary, stack-only)

The shipped bundle is night-calibrated; a day feed fails the template self-test
(`median frame peak < 0.5` → `template_lost`) by design. For a daylight
stack check only, set in `.env`:

```bash
SMOKE_TEST_RELAX_TEMPLATE=1
docker compose up -d --force-recreate model pipeline
```

This relaxes `template_lost_peak` to `0.05` at runtime (logged once as
`SMOKE TEST MODE`) without touching `model/`. Pass criteria for today:

```bash
docker compose ps                                    # both Up, pipeline younger than model
docker compose logs --tail=100 pipeline | grep -E "dense pipeline starting|connection refused|model client could not start"
docker compose logs --tail=50 model | grep -E "SMOKE TEST|startup self-test|bundle loaded"
curl -fsS http://127.0.0.1:8080/health/live         # must be 200
curl -sS http://127.0.0.1:8080/health/ready         # expect false/not_ready today — OK
curl -sS http://127.0.0.1:8080/metrics | grep -E "frames|dependency|alerts"
```

Expect: `dense pipeline starting`, zero `connection refused` after ~30 s, no
`startup self-test FAILED`, `frames_total` climbing, `ready=false`. Tier-1
frames flow so transport, polling, storage, and the viewer can be verified;
no loop lock and no meaningful alerts. Detections/alerts in this mode are
meaningless: back up then wipe `data/alerts.db` + `evidence/` afterwards,
set the flag back to `0`, and recreate both services before any real
evaluation.

## Updating a server that already runs an older version

Yes — after `git pull` you must rebuild, because this update changes the
pipeline binary (Go), the model code (Python), and `docker-compose.yml`
itself. A plain `up -d` would keep running the old images. From the project
directory:

```bash
git pull
docker compose build               # rebuilds pipeline + model with the new code
docker compose up -d               # pipeline waits for model health before starting
docker compose ps
docker compose logs -f --tail=100 model pipeline
```

If only the model changed, `docker compose build model` + 
`docker compose up -d --force-recreate model pipeline` is enough (recreate the
pipeline too so it re-resolves the model IP). After any update, re-check
`curl /health/live /health/ready /metrics` as above.

`/health/ready` may remain false while the model is collecting enough dense
frames to lock. The pipeline must not be used for operator alerts until it is
ready.

The pipeline applies the configured five-minute dependency timeout to camera
ingestion, model transport, and Tier-2 state polling. A persistent failure
causes a clean process exit so Compose can restart it. Evidence or SQLite write
failures are treated as fatal immediately to avoid reporting alerts that were
not durably stored.

For an offline target, build and export the versioned images while temporary
Internet access is available, then transfer the generated `dist/images/`
directory through the approved process:

```bash
tools/export-images.sh
tools/import-images.sh dist/images
```

## Stop, update, and backup

```bash
docker compose down
cp data/alerts.db "data/alerts.db.$(date -u +%Y%m%dT%H%M%SZ).bak"
docker compose build --pull
docker compose up -d
```

The pipeline database migration preserves the old MVP alerts and makes
confidence nullable for the v2 model. Keep the SQLite database and `evidence/`
directory together when backing up or restoring.

## Night run (20:00–04:00 Casablanca, 1000-alert cap)

`config/config.yaml` carries the night-run shape:

```yaml
schedule:
  enabled: true
  start: "20:00"
  stop: "04:00"
  timezone: "Africa/Casablanca"
retention:
  days: 365
  max_alerts: 1000
```

Behavior: the process stays up around the clock (HTTP + viewer always live),
but camera lanes only run inside the window. Outside it there is no ffmpeg
process and no model traffic; `/health/ready` is false and `/api/v1/status`
reports `standby` with the resume time. At 20:00 lanes start automatically
(first detections ~20:20+ after the model locks); at 04:00 lanes stop and the
pipeline writes `data/night-<date>.stats.json` plus the dawn report
`data/reports/night-<date>.md` (totals, pending/confirmed, per-godet table,
cap line). Regenerate any report any time with:

```bash
go run ./cmd/night-report -db data/alerts.db -evidence evidence \
  -start 2026-09-21T20:00:00+01:00 -end 2026-09-22T04:00:00+01:00 \
  -stats data/night-2026-09-21.stats.json -out data/reports/night-2026-09-21.md
```

At 1000 stored alerts the pipeline stops storing new keys (pending→confirmed
upserts of known keys still go through) and counts every drop
(`clinker_vision_alerts_dropped_cap_total`, warn log, `storage cap reached`
in status detail). The cap resets only by manual wipe after the dawn review:

```bash
docker compose down
cp data/alerts.db "data/alerts.db.$(date -u +%Y%m%dT%H%M%SZ).bak"
rm -f data/alerts.db && rm -rf evidence/2026
docker compose up -d
```

To prove the full cycle today without waiting for night, use a 10-minute
test window in a scratch config (never commit it), watch idle → start →
stop → report appear, then restore the real window.

## Current integration boundary

CAM-1 is the only enabled dense lane. Tier-1 responses are measurements and are
never rendered as alerts. Tier-2 state events are polled and upserted by the
stable key `DAMAGE:<godet_id>:<loop>`, so retries and pending-to-confirmed
updates do not create duplicate operator alerts.

Candidate frame IDs are returned by the model and matched against the
pipeline's sparse candidate-frame cache. If a candidate has aged out, the
generated alert records the actual fallback frame used.
