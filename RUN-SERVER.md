# Clinker Vision on the plant server: the icons

Everything day to day is done with 6 icons on the server's Desktop. No commands.

| Icon | What it does |
|---|---|
| **Clinker Vision - Update** | Installs the newest version. It downloads it while the system keeps running, backs up the alerts and settings, restarts, and checks that it runs. **If the new version does not start, it goes back to the previous one by itself.** |
| **Clinker Vision - Status** | Shows the installed version and, for each camera, whether it runs (`ok`, `standby` outside its hours, or a problem). |
| **Clinker Vision - Viewer** | Opens the alerts page in the browser. |
| **Clinker Vision - Save logs** | Makes one file on the Desktop (`clinker-logs-<date>.tar.gz`) to send when something is wrong. Camera passwords are hidden. |
| **Clinker Vision - Roll back** | Goes back to the version that ran before the last update (asks first). Alerts are kept. |
| **Clinker Vision - Repair** | When it is stuck or not working (for example after a power cut): records the state, fixes everything it can, restarts, tests the cameras. Asks your password. |

The window stays open at the end: read the last line (green ✔ = good, red ✘ = problem), then press Enter.

## When to click Update

When you are told a new version is published. The update takes 1-3 minutes (the first one longer: it
downloads about 2 GB); the cameras are not watched during the restart (about 1 minute).

## If something is wrong (stuck, 0 frames, not healthy, after a power cut...)

Click **Clinker Vision - Repair** (it asks your password). Or, in a terminal:

```bash
sudo bash ~/clinker-vision-/deploy/server/cv-repair
```

It first records everything (why the server stopped, crashes, logs; passwords hidden) into
`clinker-repair-<date>.tar.gz` on the Desktop, then fixes what it can: Docker running and started at
boot, project files on the newest version (hand edits saved in `backups/`), folder and settings
permissions, damaged databases moved aside, disk space, images, model memory, a clean restart, and a
boot service so the system starts by itself after every restart of the server. It then tests every
camera and says in plain words which one does not answer. **Send the report file.**

Notes:
- A camera that says `standby` is outside its hours: normal.
- While one camera does not answer, the pipeline restarts every 5 minutes and all cameras show 0 frames
  meanwhile: the Repair output names the camera to check (NVR on? network? password?).
- If it started right after an update, **Roll back** is also possible.

If the Repair icon or file is not there yet (a server set up before 2026-09-30), this one command
downloads the newest repair script and runs it:

```bash
curl -fsSL https://raw.githubusercontent.com/RedhaIWNL/clinker-vision-/cam4-day/deploy/server/cv-repair -o /tmp/cv-repair && sudo bash /tmp/cv-repair ~/clinker-vision-
```

Backups (alerts database, settings, model state) are in the project folder under `backups/`, one folder
per update; the last 10 are kept.

---

## One-time setup (already done once per server)

In a terminal, in the project folder:

```bash
cd ~/clinker-vision-
git fetch origin && git checkout cam4-day && git pull     # the last manual update
bash deploy/server/install.sh                            # checks the tools, creates the icons
```

Then double-click **Clinker Vision - Update**. The first time, Ubuntu may ask to trust the icons:
right-click each one and choose **Allow Launching**.

If the setup says Docker may not be used: `sudo usermod -aG docker $USER`, log out and in, run it again.

## One camera at a time

This server (6 cores) cannot run CAM-1, CAM-3 and CAM-4 at 25 frames/s together: each would get about
5 frames/s and the model would read the chain as stopped (2026-09-30). Their hours must not overlap:

```bash
sudo bash ~/Projects/clinker-vision-/deploy/server/cv-schedule
```

sets CAM-3 09:00-12:30, CAM-4 12:30-16:00, CAM-1 16:00-09:00 (backup of the settings in `backups/`)
and restarts the pipeline. **Repair** warns when hours overlap. If frames are lost anyway, Status says
`CAMERA STREAM LOSING FRAMES` (not "conveyor stopped").

**Easier: the Viewer's *Camera hours* box** (left column, below the godet number). For each camera:
*Default*, *These hours* (start / stop, may pass midnight), *Always on* or *Off*. **Save hours**:
cameras start or stop within 15 seconds, no restart, nothing to type in a terminal. The box warns when
two cameras' hours overlap.

*Default* (from the version after 2026.10.01) = one camera at a time, built into the system:
CAM-3 09:00-12:30, CAM-4 12:30-16:00, CAM-1 16:00-08:00 (CAM-1 was checked on night, 17:00 and 07:00
light only). It replaces the hours in `config.yaml` for these three cameras (the server's file still
had CAM-1 06:01-06:00 and CAM-3 / CAM-4 09:00-16:00, i.e. all three at once). A camera that is not set
on the page follows *Default*. To try one camera now: it *Always on*, the others *Off*; afterwards all
back to *Default*.

## Wheel alert limits

In the Viewer, **Wheels** view, box *Wheel alert limits*: the maximum godets in a row without a wheel
(default 4) and the minimum spacing between two wheels (default 3). Save: the model uses them from the
next chain loop (about 16 minutes); alarms inside the limits are hidden.

## Changing which cameras run

Still done in `config/config.yaml` (see `RUN-CAM4.md` sections 4, 4b, 4c). The file holds the camera
passwords, so only the system may read it: open it with `sudo nano config/config.yaml`. After a change,
run `deploy/server/cv-update --force` in a terminal to restart with the new settings.

## For the developer: publishing a version

```bash
tools/release.sh        # on committed + pushed work: creates the label v<date> and pushes it
```

GitHub tests the code and builds the two images (`.github/workflows/release.yml`, about 10 minutes):
`ghcr.io/redhaiwnl/clinker-vision-pipeline:<label>` and `ghcr.io/redhaiwnl/clinker-vision-model:<label>`
(the model image contains the calibration bundles). The server's Update icon picks the newest label.
The server's project folder follows the label (a detached git checkout): do not edit tracked files
there; the Update icon refuses to run over hand edits.

Without internet on the target: `CV_VERSION=<label> tools/export-images.sh` on a PC with internet, copy
`dist/images`, then `docker load -i` both files on the target and set `CV_VERSION=<label>` in `.env`.
