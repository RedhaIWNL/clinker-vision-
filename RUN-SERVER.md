# Clinker Vision on the plant server: the icons

Everything day to day is done with 5 icons on the server's Desktop. No commands.

| Icon | What it does |
|---|---|
| **Clinker Vision - Update** | Installs the newest version. It downloads it while the system keeps running, backs up the alerts and settings, restarts, and checks that it runs. **If the new version does not start, it goes back to the previous one by itself.** |
| **Clinker Vision - Status** | Shows the installed version and, for each camera, whether it runs (`ok`, `standby` outside its hours, or a problem). |
| **Clinker Vision - Viewer** | Opens the alerts page in the browser. |
| **Clinker Vision - Save logs** | Makes one file on the Desktop (`clinker-logs-<date>.tar.gz`) to send when something is wrong. Camera passwords are hidden. |
| **Clinker Vision - Roll back** | Goes back to the version that ran before the last update (asks first). Alerts are kept. |

The window stays open at the end: read the last line (green ✔ = good, red ✘ = problem), then press Enter.

## When to click Update

When you are told a new version is published. The update takes 1-3 minutes (the first one longer: it
downloads about 2 GB); the cameras are not watched during the restart (about 1 minute).

## If something is wrong

1. Click **Status**. A camera that says `standby` is outside its hours: normal.
2. Click **Save logs** and send the file.
3. If it started after an update, click **Roll back**.

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
