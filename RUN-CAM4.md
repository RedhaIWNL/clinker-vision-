# Running CAM-1 + CAM-4: step by step

Run these on the plant server, in the project folder (where `docker-compose.yml` is).
Each step shows what you should see. If a step does not show it, stop there.

This version runs **CAM-1 at night** and **CAM-4 by day** in the same system, each on
its own hours. It also fixes the night problem where the model skipped every frame after
the pipeline restarted, and kept warning "median frame peak < 0.5, camera moved?".

---

## 0. Camera 1 was re-aimed: turn its old lane off

Camera 1 now looks at the chain end after the sprocket. The Camera 1 model in this system was
calibrated on the old view, so it can't work on the new one. In `config/config.yaml`, set the
CAM-1 block to `enabled: false` (keep its URL for later), then
`docker compose up -d --force-recreate pipeline`. At least one camera (CAM-4) must stay enabled.

## 1. Get the new version

```bash
cd ~/clinker-vision-          # your project folder
git fetch origin
git checkout cam4-day
git pull
git log --oneline -3          # the top commits mention CAM-4
```

## 2. Back up the alerts (safe to skip on a test machine)

```bash
docker compose down
cp data/alerts.db "data/alerts.db.$(date -u +%Y%m%dT%H%M%SZ).bak"
```

## 3. Check that the CAM-4 video stream answers

Replace `PASSWORD` with the NVR password (URL-encode it if it contains `@ : / ? #`):

```bash
ffprobe -rtsp_transport tcp -v error \
  -show_entries stream=codec_name,width,height,r_frame_rate \
  -of default=noprint_wrappers=1 \
  'rtsp://admin:PASSWORD@192.168.1.200:554/cam/realmonitor?channel=4&subtype=0'
```

You must see **`width=2688`** and **`height=1520`** (and `r_frame_rate=25/1`). Any other
size: the model refuses the frames. Check the channel (NVR channel 4 = camera
192.168.1.144) and that `subtype=0` (main stream).

## 4. Turn CAM-4 on in the config

```bash
sudo nano config/config.yaml
```

Find the `CAM-4` block and replace it with this. Put the real password in the URL and
choose the hours you want to test:

```yaml
  - id: "CAM-4"
    enabled: true
    nvr_rtsp_url: "rtsp://admin:PASSWORD@192.168.1.200:554/cam/realmonitor?channel=4&subtype=0"
    sample_interval_seconds: 1
    queue_capacity: 64
    schedule:
      enabled: true
      start: "09:00"
      stop: "16:00"
      timezone: "Africa/Casablanca"
```

Leave the CAM-1 block and the global `schedule:` (20:00–04:00) as they are: CAM-1 has no
`schedule` of its own, so it keeps following the global night window.

**To test right now** (whatever the time), set CAM-4's `start` to a few minutes from now
and `stop` a few hours later. Change it back afterwards.

Keep the file protected (the pipeline refuses to start otherwise):

```bash
sudo chown 65532:65532 config/config.yaml
sudo chmod 600 config/config.yaml
```

## 5. Build and start

```bash
docker compose build
docker compose up -d
docker compose ps
```

`model` and `pipeline` must both be `Up` (the model shows `healthy`).

## 6. Check it started correctly

```bash
docker compose logs model | grep -E "CAM-4 enabled|CAM-4 bundle loaded|startup failed"
docker compose logs pipeline | grep -E "lane starting|camera idle|configuration rejected"
```

You should see:

- `CAM-4 bundle loaded ... godets=1210` and `CAM-4 enabled model_version=1c53472e+...`
- in the pipeline: `lane starting` for each camera inside its hours, or `camera idle outside
  operating window` for the other.

If you see `configuration rejected`, the message says which line of `config.yaml` is wrong.

## 7. Watch it work

Open the viewer on the server: **http://127.0.0.1:8080**. The status line shows one
entry per camera, for example `CAM-1: standby | CAM-4: ok (12,345 frames)`.

Or from the terminal:

```bash
curl -s http://127.0.0.1:8080/api/v1/status | python3 -m json.tool | grep -E '"camera_id"|"status"|"detail"|frames_total|godets_confirmed|retained_frames_total'
```

What to expect for CAM-4 after its window opens:

| Time after start | Status / detail | Meaning |
|---|---|---|
| 0–1 min | `not_ready`: locking onto the chain map | normal |
| ~1 min | `ok`: history building: N passes of 300 needed | chain found |
| ~4 min | `ok`: all clear | judging begins |
| ~20–35 min | `godets_confirmed` > 0, alerts appear | a godet needs 2 strong passes; one chain loop is about 16 min of moving chain |

Warnings you might see:

- `conveyor stopped`: the chain is not moving. Normal during stops.
- `VIEW LOST`: the camera sees no chain movement (moved, blocked, too dark). Look at the camera.
- `lost the chain map: re-locking`: it re-finds its place in about 1 minute.
- `POPULATION`: more than 15 % of godets are flagged. Suspect the light or view, not the chain.

## 8. Check the alerts

In the viewer, filter by camera **CAM-4**. Each CAM-4 alert shows:

- the godet number (the same numbers as the review pages; godet 0 appears as **1210**);
- a red box on the fault spot;
- `severity` (≥ 1.65 = confirmed) and `passes_seen`.

If an alert says `"evidence": "latest_frame"`, its original picture was no longer cached,
so the image shows the live view and not the flagged plate.

At the end of each CAM-4 window, a report is written:

```bash
ls data/reports/
cat data/reports/CAM-4-day-$(date +%F).md
```

CAM-1 keeps its `night-<date>.md` reports.

## 8b. Wheel (galet) alerts

CAM-4 also watches the wheels under the chain. There is normally one wheel every 4 godets. It
raises three kinds of alert (camera CAM-4, target **galet**):

| Alert | Meaning | When |
|---|---|---|
| `WHEEL_GAP` | 5 or more godets in a row without a wheel | once per place (it's how the chain is built) |
| `WHEEL_DENSITY` | 2 or more wheels within 3 godets | once per place |
| `WHEEL_MISSING` | a wheel that was there is gone on the last 2 passes | when it happens (a change: check the chain) |

The godet number is the first godet of the place; the measurements give `first_godet`,
`last_godet`, `godets` and `wheels`. The picture is the unrolled chain around the place, with
godet numbers, wheels boxed and the flagged stretch framed.

- The first wheel alerts appear after **2 chain loops** (about 35 minutes of moving chain).
- On the 2026-09-07 recordings: about 14 gap places and 9–10 density places per loop, and no
  missing wheel.
- `WHEEL_GAP` and `WHEEL_DENSITY` are the fixed pattern of the chain. Mark them seen once
  they're checked; they won't come back under the same key.
- A **`WHEEL_MISSING` is the one to act on.**

## 9. If something goes wrong

```bash
docker compose logs --tail=200 model pipeline > logs-$(date +%F-%H%M).txt
```

Send that file. To go back to the previous version:

```bash
docker compose down
git checkout main
docker compose build
docker compose up -d
```

## Notes

- Both cameras can run at the same time. On a 16-core test PC the model kept 25 fps on
  each camera with its 2-CPU limit (about 1 core used). The pipeline needs about 2 GB of RAM
  for two cameras, mostly the two FFmpeg decoders.
- CAM-4 is calibrated on 2026-09-07 at 10:00 and 12:00 light. Early morning, late afternoon
  and cloudy days are untested: watch the status and the first alerts before widening the
  hours.
- CAM-4 alerts count towards the same 1000-alert cap as CAM-1.
