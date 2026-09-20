# Clinker Vision deployment information collection

Run these commands on the Ubuntu machine that will run Clinker Vision. Send the completed template at the end. Never send passwords, tokens, private keys, or unredacted RTSP URLs.

## Ubuntu host

~~~~bash
lsb_release -a
cat /etc/os-release
hostnamectl
uname -a
uname -m
nproc
free -h
df -h
timedatectl
hostname -I
docker --version
docker compose version
docker info --format 'Server={{.ServerVersion}} Storage={{.Driver}} CPUs={{.NCPU}} Memory={{.MemTotal}}'
~~~~

Record the Ubuntu version, hostname, architecture, CPU cores, RAM, available disk, timezone, host IP, Docker version, and Compose version. Also state whether Docker works without `sudo`.

If Docker is not installed, record that fact. If needed, install it using the official Docker Ubuntu instructions, then verify with:

~~~~bash
docker ps
~~~~

## Git repository URL and version

Run from the project directory:

~~~~bash
cd /home/redha/Projects/clinker_vision
git remote -v
git remote get-url origin
git branch --show-current
git describe --tags --always --dirty
git rev-parse HEAD
git tag --points-at HEAD
git status --short
~~~~

Record the repository URL, branch, tag, commit, and whether the working tree is clean.

If the project has not been pushed yet, copy the URL from the GitHub, GitLab, or Bitbucket repository page. Examples:

~~~~text
https://github.com/ORGANIZATION/REPOSITORY.git
git@github.com:ORGANIZATION/REPOSITORY.git
https://gitlab.com/ORGANIZATION/REPOSITORY.git
~~~~

For a private repository, do not send credentials here. Use an SSH deploy key or configure Git credentials on the Ubuntu machine. Test access with:

~~~~bash
git ls-remote --heads --tags REPOSITORY_URL
~~~~

## Find the NVR IP

The preferred method is the NVR network page or the router DHCP client list. Record the NVR hostname, IP address, MAC address, and RTSP port.

From Ubuntu, inspect the local network:

~~~~bash
ip -4 addr
ip route
ip neigh
arp -a 2>/dev/null || true
~~~~

Test a suspected NVR:

~~~~bash
ping -c 3 NVR_IP
nc -vz NVR_IP 554
~~~~

If `nmap` is available, find devices exposing RTSP on the local subnet:

~~~~bash
nmap -p 554 --open 192.168.1.0/24
~~~~

Replace the subnet with the one shown by `ip -4 addr` and `ip route`. Only scan networks you administer.

## CAM-1 RTSP details

Get the stream information from the NVR web interface, camera settings, NVR manual, or an existing VLC/FFmpeg setup. Record:

~~~~text
Camera mapped to CAM-1:
Physical camera location:
NVR IP/hostname:
RTSP port:
RTSP stream path:
Username: no password
Stream: main/substream
Codec: H.264/H.265
Resolution:
FPS:
Transport: TCP/UDP
~~~~

The URL format is usually:

~~~~text
rtsp://USERNAME:REDACTED@NVR_IP:554/STREAM_PATH
~~~~

Test port access:

~~~~bash
nc -vz NVR_IP RTSP_PORT
~~~~

Test the video stream with FFprobe. Do not send the command output if it contains credentials:

~~~~bash
ffprobe -hide_banner -rtsp_transport tcp \
  -i 'rtsp://USERNAME:REDACTED@NVR_IP:554/STREAM_PATH' \
  -select_streams v:0 \
  -show_entries stream=codec_name,width,height,r_frame_rate,avg_frame_rate \
  -of json
~~~~

The result should report a video stream, codec, resolution, and frame rate. If TCP fails and the NVR requires UDP, repeat with:

~~~~bash
ffprobe -hide_banner -rtsp_transport udp \
  -i 'rtsp://USERNAME:REDACTED@NVR_IP:554/STREAM_PATH' \
  -select_streams v:0 \
  -show_entries stream=codec_name,width,height,r_frame_rate,avg_frame_rate \
  -of json
~~~~

Optional live test:

~~~~bash
ffplay -rtsp_transport tcp 'rtsp://USERNAME:REDACTED@NVR_IP:554/STREAM_PATH'
~~~~

If the username or password contains special URL characters, they must be URL-encoded. Do not paste the password into the information sent back.

## Final model image and tag

Ask the model developer for:

~~~~text
Image registry:
Image repository:
Complete image reference:
Image tag:
Image digest:
Model version:
gRPC port:
Health/readiness behavior:
CPU/GPU requirement:
RAM requirement:
Expected latency:
Required environment variables:
~~~~

The preferred image reference looks like:

~~~~text
registry.example.com/organization/clinker-vision-model:VERSION
~~~~

If the image is already installed on Ubuntu:

~~~~bash
docker image ls --digests
docker image inspect IMAGE_REFERENCE \
  --format 'ID={{.Id}} Tags={{json .RepoTags}} Digests={{json .RepoDigests}} Size={{.Size}}'
~~~~

If it is defined in Compose:

~~~~bash
cd /home/redha/Projects/clinker_vision
docker compose config
~~~~

Confirm the `model:` service no longer uses the development mock image:

~~~~text
clinker-vision-mock-model:dev
~~~~

If the developer provides source instead of an image, collect the source URL, branch/tag/commit, Dockerfile path, weights location, weights SHA256, build command, and runtime command.

The model must implement:

~~~~text
Package: clinkervision.inference.v1
Service: InferenceService
RPC: Infer(stream InferenceRequest) returns (stream InferenceResponse)
Port: 50051 unless documented otherwise
~~~~

## Storage and retention

Show the current configured values:

~~~~bash
cd /home/redha/Projects/clinker_vision
grep -n -A8 '^storage:' config/config.yaml 2>/dev/null || true
grep -n -A3 '^retention:' config/config.yaml 2>/dev/null || true
~~~~

For the current Compose layout:

~~~~bash
docker compose config | sed -n '/volumes:/,/read_only:/p'
~~~~

Expected container paths:

~~~~text
SQLite database: /srv/clinker-vision/data/alerts.db
Evidence images: /srv/clinker-vision/evidence
Logs: /srv/clinker-vision/logs
~~~~

Expected repository host paths:

~~~~text
Database directory: /home/redha/Projects/clinker_vision/data
Evidence directory: /home/redha/Projects/clinker_vision/evidence
Logs directory: /home/redha/Projects/clinker_vision/logs
~~~~

Check capacity and current usage:

~~~~bash
df -h /home/redha/Projects/clinker_vision
du -sh /home/redha/Projects/clinker_vision/data \
       /home/redha/Projects/clinker_vision/evidence \
       /home/redha/Projects/clinker_vision/logs 2>/dev/null || true
~~~~

The current default retention is 365 days. Record the configured retention, available disk space, whether backups are required, and the backup destination.

## Response template

Copy and complete this section. Redact credentials.

~~~~text
Ubuntu
------
Version:
Hostname:
Architecture:
CPU cores:
RAM:
Available disk:
Timezone:
Host IP:
Docker version:
Docker Compose version:
Docker works without sudo: yes/no

Git
---
Repository URL:
Branch:
Tag:
Commit:
Working tree clean: yes/no

NVR / CAM-1
-----------
NVR hostname:
NVR IP:
RTSP port:
CAM-1 physical location:
Stream path: credentials redacted
Username: optional, no password
Codec:
Resolution:
FPS:
Main or substream:
Transport:
FFprobe succeeded: yes/no

Model
-----
Image reference:
Image tag:
Image digest:
Model version:
gRPC port:
Health behavior:
CPU/GPU:
RAM:
Expected latency:
Required environment variables:

Storage
-------
Database path:
Evidence path:
Logs path:
Retention days:
Available disk:
Backup required: yes/no
Backup destination:
~~~~
