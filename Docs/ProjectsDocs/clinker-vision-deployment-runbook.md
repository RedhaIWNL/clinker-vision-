# Clinker Vision Deployment Runbook

**Project:** Clinker Bucket Elevator Vision Monitoring System  
**Deployment:** MVP, one physical Ubuntu server  
**Status:** Draft v0.1 — prepared from the September 2026 MVP decisions

## 1. Purpose

This runbook explains how to install and operate the MVP on the plant server. It is written for a person who is comfortable following commands but is not expected to know Docker, Linux services, or model deployment.

The MVP monitors the godet camera on NVR channel/CAM-1. The other five cameras remain connected to the Dahua NVR but are not processed yet.

## 2. MVP architecture

```text
Godet camera (CAM-1)
        |
        | camera video
        v
Dahua NVR (appears to be NVR4232-EI; verify exact model)
        |
        | Ethernet / RTSP
        v
Ubuntu server
  ├── Docker container: clinker-vision pipeline
  │     ├── NVR/RTSP ingestion
  │     ├── frame sampling and queueing
  │     ├── model client
  │     ├── SQLite alert database
  │     └── local web/API viewer
  └── Docker container: model service
        └── Python/OpenCV crop, resize, preprocessing, and inference
```

The pipeline sends full-frame JPEG images. It does not crop images. The model service owns all dynamic cropping, resizing, normalization, and inference.

Operators open the web viewer directly on the server. The MVP has no phone application, push notification, user login, TLS, or external backend.

## 3. Confirmed hardware baseline

- Physical HP server, approximately 2015 generation.
- Ubuntu Server LTS installed.
- Approximately 500 GB local HDD.
- 16 GB RAM.
- Xeon CPU at approximately 2.40 GHz; record the exact CPU model during installation.
- Dahua network video recorder, label appears to show `NVR4232-EI`; verify in the NVR web interface.
- Six cameras are connected to the NVR.
- Only camera/channel 1 is used by the MVP.
- The server and NVR communicate through Ethernet.
- Temporary Internet access is needed during installation to download Docker and container images. The production server should not require Internet access after installation.

## 4. Required handoff from the model owner

The preferred handoff is a Docker image, not only a model-weight file. Ask the model owner to provide:

1. A versioned Docker image, for example `clinker-model:2026.09.1`.
2. A small `docker-compose.yml` example or the exact container start command.
3. The gRPC service implementing `clinkervision.inference.v1`.
4. The `.proto` file used to generate the client.
5. A sample request and response using a real or sanitized full-frame JPEG.
6. The required CPU, RAM, and storage limits.
7. The expected input JPEG size/quality and maximum supported image dimensions.
8. Confirmation that training and serving use the same Python/OpenCV crop, resize, and normalization code.
9. A CPU benchmark showing frames per second and average/worst-case latency.
10. The model version string returned in every response.
11. A test image and expected response for a godet misalignment case and a crack case.

For the MVP, the model only needs to return `GODET` detections. The expected fault values are `MISALIGNMENT` and `CRACK`, with the possibility of other fault values if the model produces them. Each detection must include raw confidence and a bounding box normalized against the full input frame.

## 5. Required installation information

Do not place passwords in this document. Record them in the protected deployment configuration on the server.

Before installation, obtain:

- NVR IP address.
- NVR web username and password.
- NVR channel-1 RTSP URL or the information needed to construct it.
- Exact NVR model and firmware version.
- Server IP configuration.
- Temporary Internet access method for the server.
- Exact model Docker image and version.

The NVR web interface is used to verify the IP, channel configuration, camera preview, and RTSP settings.

## 6. Installation preparation

### 6.1 Verify Ubuntu

Log in locally to the server and run:

```bash
lsb_release -a
uname -a
free -h
df -h
lscpu
ip addr
```

Record the output in the deployment record. Confirm that:

- Ubuntu is an LTS release.
- At least 100 GB is free on the 500 GB disk.
- At least 8 GB RAM is available for the application after the operating system is running.
- The CPU architecture is `amd64`/`x86_64`.
- The server has a stable local IP address.

### 6.2 Update Ubuntu

During the temporary Internet-enabled installation window:

```bash
sudo apt update
sudo apt upgrade -y
sudo apt install -y ca-certificates curl git jq ffmpeg net-tools
```

Reboot if Ubuntu requests it:

```bash
sudo reboot
```

### 6.3 Install Docker

Install Docker Engine and the Docker Compose plugin using Docker's official Ubuntu installation instructions. Verify the installation:

```bash
docker --version
docker compose version
sudo systemctl enable --now docker
```

Add the deployment user to the Docker group only if the plant's access policy permits it. Otherwise, run Docker commands with `sudo`.

### 6.4 Create application directories

```bash
sudo mkdir -p /srv/clinker-vision/{config,data,evidence,logs,images}
sudo mkdir -p /var/log/clinker-vision
sudo chmod 700 /srv/clinker-vision/config
```

The directories have these purposes:

- `/srv/clinker-vision/config` — protected configuration and NVR credentials.
- `/srv/clinker-vision/data` — SQLite database and runtime state.
- `/srv/clinker-vision/evidence` — alert JPEGs.
- `/srv/clinker-vision/logs` — optional exported logs.
- `/srv/clinker-vision/images` — offline Docker image archives.

## 7. Install the application package

The deployment package should be copied to the server using a USB drive or another approved local transfer method. It should contain:

```text
clinker-vision-deployment/
├── docker-compose.yml
├── .env.example
├── pipeline/
│   └── pipeline-image.tar
├── model/
│   └── model-image.tar
├── proto/
│   └── inference.proto
├── config/
│   └── config.example.yaml
└── README.md
```

Copy the package to `/srv/clinker-vision/` and load the images:

```bash
cd /srv/clinker-vision
sudo docker load -i images/pipeline-image.tar
sudo docker load -i images/model-image.tar
sudo docker images
```

If images are delivered through a private registry instead, authenticate during the temporary Internet window, pull the exact versioned tags, and record the image digests before disconnecting the server.

## 8. Configure the NVR and pipeline

Create the production configuration from the example supplied by the application team:

```bash
sudo cp config/config.example.yaml config/config.yaml
sudo nano config/config.yaml
```

Set at minimum:

```yaml
server:
  bind_address: "127.0.0.1"
  web_port: 8080

cameras:
  - id: "CAM-1"
    enabled: true
    nvr_rtsp_url: "<protected-channel-1-rtsp-url>"
    sample_interval_seconds: 2

  - id: "CAM-2"
    enabled: false
  - id: "CAM-3"
    enabled: false
  - id: "CAM-4"
    enabled: false
  - id: "CAM-5"
    enabled: false
  - id: "CAM-6"
    enabled: false

model:
  grpc_address: "model:50051"
  request_timeout_seconds: 5
  confidence_threshold: 0.85

storage:
  sqlite_path: "/var/lib/clinker-vision/alerts.db"
  evidence_path: "/srv/clinker-vision/evidence"
  jpeg_quality: 85

retention:
  days: 365
```

The actual configuration field names must match the released pipeline image. The example above describes the required values, not a final implementation schema.

Protect the file:

```bash
sudo chown root:root config/config.yaml
sudo chmod 600 config/config.yaml
```

Do not commit or copy this file into Git, Docker images, screenshots, or logs.

## 9. Test the NVR before starting Docker

1. Connect the NVR and server with Ethernet.
2. Confirm that the server can reach the NVR:

```bash
ping -c 4 <NVR_IP>
```

3. Open the NVR web interface from the server:

```text
http://<NVR_IP>
```

4. Log in using the plant-provided credentials.
5. Confirm that camera/channel 1 shows the godet view.
6. Confirm the channel-1 RTSP URL.
7. Test the RTSP stream with FFmpeg or the pipeline's built-in test command.

The password must never appear in a shell command saved in shell history. Use the protected application configuration or a temporary interactive prompt.

## 10. Start the MVP

From the deployment directory:

```bash
cd /srv/clinker-vision
sudo docker compose up -d
```

Check container status:

```bash
sudo docker compose ps
```

Follow logs during the first start:

```bash
sudo docker compose logs -f --tail=200
```

The expected startup sequence is:

1. Model container starts and reports ready.
2. Pipeline connects to the model service.
3. Pipeline connects to NVR channel 1.
4. Frames begin entering the sampler and queue.
5. Model responses are correlated by `frame_id`.
6. The local web viewer becomes available.

## 11. Verify the installation

On the server itself, open:

```text
http://127.0.0.1:8080
```

Verify all of the following:

- The web page loads.
- CAM-1 is shown as connected.
- Frames are being sampled approximately every 1–2 seconds.
- The model reports its version.
- A normal frame creates no alert.
- A test detection creates one alert.
- The evidence JPEG is saved under `/srv/clinker-vision/evidence`.
- The alert appears in the local feed.
- The bounding box is correctly drawn on the full frame.
- The system does not process CAM-2 through CAM-6.

Use the model team's test image or a controlled test frame. Do not generate a false production alarm during plant operation without approval.

## 12. Failure behavior

The MVP intentionally does not preserve frames that the model has not processed. The NVR remains the source of recorded camera history.

The pipeline must distinguish these conditions in its logs:

- NVR unreachable.
- CAM-1 RTSP stream unavailable.
- Model service unavailable.
- Model request timeout.
- Invalid model response.
- SQLite/evidence write failure.

If a required dependency remains unavailable for the configured failure period, the system stops completely and records the reason. The initial recommended failure period is 5 minutes, but the final value must be set in configuration.

To stop manually:

```bash
cd /srv/clinker-vision
sudo docker compose stop
```

To start again:

```bash
sudo docker compose start
```

To restart after configuration changes:

```bash
sudo docker compose down
sudo docker compose up -d
```

## 13. Internet disconnection after installation

After Docker images and required packages have been installed:

1. Confirm that both containers start without downloading anything.
2. Confirm that the model image and pipeline image exist locally.
3. Confirm that the NVR connection works without Internet access.
4. Disconnect or disable the temporary Internet connection.
5. Restart the stack and repeat the verification in Section 11.

The production MVP must not depend on Docker Hub, a cloud model API, or any external notification service.

## 14. Daily operator procedure

Operators only need to:

1. Log in to the Ubuntu server.
2. Open the browser.
3. Open `http://127.0.0.1:8080`.
4. Review new alerts and their evidence images.

There are no operator accounts and no push notifications in the MVP.

## 15. Recovery procedure

If the web viewer is unavailable:

```bash
cd /srv/clinker-vision
sudo docker compose ps
sudo docker compose logs --tail=200
sudo docker compose restart
```

If the problem continues, stop the stack and record:

- Date and time.
- NVR status.
- Container status.
- Last error in the logs.
- Whether the server disk is full.
- Whether the model container reports ready.

Do not delete `/srv/clinker-vision/data` or `/srv/clinker-vision/evidence` during troubleshooting.

## 16. Production limitations accepted for MVP

- No TLS.
- No user authentication.
- No phone/mobile access.
- No push notifications.
- No NAS or NFS evidence storage.
- No separate backup of the SQLite database or evidence images.
- Frames not processed by the model are not replayed.
- Only CAM-1 is enabled.
- No automatic SCADA/PLC control.

The NVR's own camera recording is not a backup of the model's alert database or evidence overlays. If an alert is deleted from the server, it cannot be reconstructed automatically from the NVR recording in this MVP.

## 17. Deployment sign-off

The deployment owner must record:

- Exact server hostname and IP.
- Ubuntu version.
- Exact CPU model.
- Docker version.
- Pipeline image tag and digest.
- Model image tag and digest.
- NVR model, IP, and firmware.
- CAM-1 RTSP test result.
- Model benchmark result.
- Web viewer URL.
- Date of installation.
- Installer name.
- Known limitations accepted by the project owner.

The project owner is responsible for final approval of this runbook and the MVP deployment.
