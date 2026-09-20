from pathlib import Path

from docx import Document
from docx.enum.section import WD_SECTION
from docx.enum.text import WD_BREAK
from docx.shared import Inches, Pt


ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "Docs" / "ProjectsDocs"


def setup(doc):
    styles = doc.styles
    styles["Normal"].font.name = "Arial"
    styles["Normal"].font.size = Pt(10)
    for section in doc.sections:
        section.top_margin = Inches(0.7)
        section.bottom_margin = Inches(0.7)
        section.left_margin = Inches(0.75)
        section.right_margin = Inches(0.75)


def title(doc, lines):
    for line in lines:
        p = doc.add_paragraph()
        p.alignment = 1
        run = p.add_run(line)
        run.bold = True if len(lines) == 1 else False
        run.font.size = Pt(16 if len(lines) == 1 else 12)


def heading(doc, text, level=1):
    doc.add_heading(text, level=level)


def para(doc, text):
    doc.add_paragraph(text)


def bullets(doc, items):
    for item in items:
        doc.add_paragraph(item, style="List Bullet")


def numbered(doc, items):
    for item in items:
        doc.add_paragraph(item, style="List Number")


def table(doc, headers, rows):
    t = doc.add_table(rows=1, cols=len(headers))
    t.style = "Table Grid"
    for cell, value in zip(t.rows[0].cells, headers):
        cell.text = str(value)
    for row in rows:
        cells = t.add_row().cells
        for cell, value in zip(cells, row):
            cell.text = str(value)
    doc.add_paragraph()
    return t


def code(doc, text):
    p = doc.add_paragraph()
    p.style = "No Spacing"
    run = p.add_run(text)
    run.font.name = "Courier New"
    run.font.size = Pt(8)


def footer(doc, version):
    for section in doc.sections:
        p = section.footer.paragraphs[0]
        p.alignment = 1
        p.text = f"Clinker Vision — {version} — Draft"


def save(doc, filename, version):
    setup(doc)
    footer(doc, version)
    doc.save(OUT / filename)


def build_srs():
    d = Document()
    title(d, [
        "SOFTWARE REQUIREMENTS SPECIFICATION",
        "Clinker Bucket Elevator Vision Monitoring System",
        "MVP: local physical server deployment",
        "Document Status: Draft v0.5 — revised to match the approved MVP baseline",
    ])
    table(d, ["Version", "Status", "Summary"], [[
        "0.5", "Draft", "Rebased on the physical HP server, direct NVR Ethernet connection, Docker deployment, CAM-1 godet MVP, local web viewer, and local evidence storage."
    ]])

    heading(d, "1 Introduction")
    heading(d, "1.1 Purpose", 2)
    para(d, "This document specifies the MVP requirements for monitoring a bucket elevator at a cement plant. The system receives camera video through a Dahua network video recorder, samples frames, sends full frames to an external model service, stores qualifying alerts and evidence, and presents the results in a local web viewer.")
    heading(d, "1.2 MVP scope", 2)
    para(d, "Six cameras are connected to the NVR, but only camera/channel CAM-1 is enabled in the MVP. CAM-1 observes godets. The initial model fault scope is misalignment and crack detection; the model returns the fault type. The architecture keeps the other five cameras configurable for later phases.")
    heading(d, "1.3 Explicit MVP boundaries", 2)
    bullets(d, [
        "The system runs on one physical HP server with Ubuntu Server LTS; no VMware VM is required.",
        "The NVR connects to the server over Ethernet on a private camera network; it is not assumed to be on the plant OT network.",
        "The pipeline and model run as Docker containers on the same server.",
        "The pipeline sends full-frame JPEG images and performs no crop. The model owns crop, resize, normalization, and inference.",
        "The web viewer is opened directly on the server. There is no mobile app, push notification, external backend, user authentication, or TLS in the MVP.",
        "Evidence and SQLite data are stored on the server's local 500 GB disk. NAS/NFS is deferred.",
        "Frames that the model does not process are not replayed. The NVR recording remains the source of camera history.",
    ])

    heading(d, "2 System context")
    para(d, "The six plant cameras feed the Dahua NVR. The NVR records the camera streams and exposes channel streams. The Ubuntu server pulls CAM-1 over Ethernet. The pipeline samples approximately one frame every two seconds, calls the model service, and displays/stores only qualifying detections.")
    table(d, ["Component", "MVP responsibility"], [
        ["Camera/NVR", "Capture and retain camera video; expose CAM-1 as RTSP."],
        ["Pipeline container", "RTSP ingestion, sampling, full-frame JPEG preparation, queueing, model client, alert persistence, evidence overlay, local REST API, and web viewer."],
        ["Model container", "Python/OpenCV preprocessing and godet inference; returns raw detections and model version over gRPC."],
        ["Server disk", "SQLite database, local evidence JPEGs, Docker images, logs, and configuration."],
        ["Operator", "Uses a browser directly on the server to review the local alert feed and evidence."],
    ])

    heading(d, "3 Functional requirements")
    heading(d, "3.1 NVR and video ingestion", 2)
    table(d, ["ID", "Requirement", "Priority"], [
        ["FR-ING-1", "The system shall connect to the NVR's CAM-1 RTSP stream using the plant-provided NVR IP, credentials, and channel URL.", "Must"],
        ["FR-ING-2", "The system shall run on the physical Ubuntu server and shall not require a VM.", "Must"],
        ["FR-ING-3", "The system shall decode CAM-1 frames and send full frames to the queue without ROI cropping.", "Must"],
        ["FR-ING-4", "The system shall detect and log NVR, RTSP, authentication, and decode failures.", "Must"],
        ["FR-ING-5", "The configuration shall contain all six camera entries, with CAM-1 enabled and CAM-2 through CAM-6 disabled in the MVP.", "Must"],
    ])
    heading(d, "3.2 Sampling and queueing", 2)
    table(d, ["ID", "Requirement", "Priority"], [
        ["FR-QUE-1", "The pipeline shall sample approximately one frame per two seconds from CAM-1; the interval shall be configurable.", "Must"],
        ["FR-QUE-2", "Each enabled camera shall have a bounded in-memory queue.", "Must"],
        ["FR-QUE-3", "When the queue is full, the oldest frame shall be discarded so ingestion does not build an unlimited backlog.", "Must"],
        ["FR-QUE-4", "Frame drops shall be counted and visible in logs or metrics.", "Should"],
    ])
    heading(d, "3.3 Model interface", 2)
    table(d, ["ID", "Requirement", "Priority"], [
        ["FR-INF-1", "The pipeline shall call the model through the agreed gRPC streaming contract.", "Must"],
        ["FR-INF-2", "The pipeline shall send full-frame JPEG bytes, camera_id, frame_id, captured_at, and sequence_no.", "Must"],
        ["FR-INF-3", "The model service shall perform dynamic crop, resize, normalization, and inference.", "Must"],
        ["FR-INF-4", "For the MVP, detections shall target GODET and shall include CRACK or MISALIGNMENT when applicable, raw confidence, full-frame normalized bounding box, and model_version.", "Must"],
        ["FR-INF-5", "The pipeline shall start with a confidence threshold of 0.85 to reduce false positives; the value shall be configurable.", "Must"],
        ["FR-INF-6", "A model timeout, unavailable model, or invalid response shall be logged as an error and shall not be treated as a normal empty detection.", "Must"],
    ])
    heading(d, "3.4 Alerts and local web viewer", 2)
    table(d, ["ID", "Requirement", "Priority"], [
        ["FR-ALT-1", "Each qualifying model detection shall create one alert. No deduplication or cooldown is required in the MVP.", "Must"],
        ["FR-ALT-2", "Alerts shall be stored in SQLite with alert_id, detected_at, camera_id, target, fault_type, confidence, frame_id, model_version, and evidence path.", "Must"],
        ["FR-ALT-3", "The pipeline shall expose a local REST API for reading alert data.", "Must"],
        ["FR-ALT-4", "The pipeline shall expose a local web viewer on the server through the REST API.", "Must"],
        ["FR-ALT-5", "The viewer shall show alert time, fault, confidence, camera, and evidence image.", "Must"],
        ["FR-ALT-6", "The MVP shall not send push notifications or call an external notification backend.", "Must"],
    ])
    heading(d, "3.5 Evidence and storage", 2)
    table(d, ["ID", "Requirement", "Priority"], [
        ["FR-EVD-1", "For every alert, the pipeline shall save one full-frame JPEG with all detections for that frame overlaid.", "Must"],
        ["FR-EVD-2", "The overlay shall use full-frame coordinates, draw boxes in red, and label each box with fault and confidence.", "Must"],
        ["FR-EVD-3", "Evidence shall be stored on the local server disk, not NAS/NFS, using a date and camera directory layout.", "Must"],
        ["FR-EVD-4", "The JPEG quality shall start at 85 and be configurable.", "Must"],
        ["FR-EVD-5", "The MVP shall retain evidence and alert rows for 365 days, subject to available disk space.", "Should"],
    ])
    heading(d, "3.6 Resilience and shutdown", 2)
    table(d, ["ID", "Requirement", "Priority"], [
        ["FR-RES-1", "The system shall continue sampling while transient individual frames fail.", "Must"],
        ["FR-RES-2", "The system shall stop completely when a required dependency remains unavailable for the configured failure period. The initial value is five minutes.", "Must"],
        ["FR-RES-3", "The system shall log the reason and timestamp when it stops because of dependency failure.", "Must"],
        ["FR-RES-4", "The system shall restart using Docker Compose after an operator or maintainer starts it again.", "Must"],
        ["FR-RES-5", "The MVP shall not persist unprocessed frame queues across restarts.", "Must"],
    ])

    heading(d, "4 External interfaces")
    table(d, ["Interface", "MVP contract"], [
        ["NVR", "Dahua NVR channel 1 over Ethernet/RTSP. Exact IP, URL, credentials, codec, and firmware are deployment values."],
        ["Model", "gRPC streaming on the local Docker network, no TLS in MVP. Full-frame JPEG request; structured detection response."],
        ["REST API", "Local HTTP API bound to 127.0.0.1. No authentication in MVP."],
        ["Web viewer", "Local HTTP page opened on the Ubuntu server at the configured localhost port."],
        ["Storage", "Local ext4 filesystem on the server's 500 GB HDD."],
    ])

    heading(d, "5 Non-functional requirements")
    table(d, ["Category", "MVP target"], [
        ["Latency", "A qualifying detection should appear in the local viewer within a few seconds of model response."],
        ["False positives", "Prefer low false-positive behavior; begin with confidence threshold 0.85 and tune using plant footage."],
        ["Availability", "Run unattended during the test/operating window; automatic recovery and production SLA are deferred."],
        ["Scalability", "Configuration and package structure shall support enabling CAM-2 through CAM-6 later without redesign."],
        ["Security", "No network exposure beyond the server in MVP; no TLS/auth accepted only because the viewer is local."],
        ["Observability", "Logs shall include camera_id, frame_id, model status, inference latency, queue drops, and stop reason."],
        ["Maintainability", "Pipeline and model remain separate containers with independently versioned images."],
    ])

    heading(d, "6 Alert data model")
    table(d, ["Field", "MVP rule"], [
        ["alert_id", "UUID v4; unique per qualifying detection."],
        ["detected_at", "Plant-local ISO 8601 timestamp with offset."],
        ["camera_id", "CAM-1 for MVP."],
        ["observation_target", "godet."],
        ["fault_type", "CRACK or MISALIGNMENT for the intended MVP model output."],
        ["confidence", "Raw model value 0–1; pipeline threshold is configurable."],
        ["bbox", "Normalized x, y, width, height relative to the full frame."],
        ["frame_id/model_version", "Traceability to the exact model request and build."],
        ["evidence_ref", "Local relative path to the saved full-frame JPEG."],
        ["seen_at", "One shared timestamp; optional local viewer behavior retained for later implementation."],
    ])

    heading(d, "7 Detection scope and future phases")
    table(d, ["Phase", "Camera/target", "Scope"], [
        ["MVP", "CAM-1 / godet", "Misalignment and crack detection."],
        ["Phase 2", "CAM-2 / godet", "Additional godet coverage after MVP validation."],
        ["Phase 2", "CAM-3 and CAM-4 / galet", "Galet damage and missing/count checks."],
        ["Phase 2", "CAM-5 / clinker level", "Level threshold and spillage."],
        ["Future", "CAM-6", "Reserve or expansion camera."],
    ])

    heading(d, "8 Open items before field deployment")
    table(d, ["ID", "Item", "Owner"], [
        ["OI-1", "Verify exact NVR model, firmware, IP address, web login, CAM-1 RTSP URL, and codec.", "Project owner / plant"],
        ["OI-2", "Record exact server CPU model, hostname, static IP, and network adapter layout.", "Project owner"],
        ["OI-3", "Obtain the model Docker image, proto, test images, and CPU benchmark.", "Model owner"],
        ["OI-4", "Confirm the local web port and exact REST response shape.", "Project owner / developer"],
        ["OI-5", "Measure real inference latency and decide whether the two-second sample interval is safe.", "Project owner / model owner"],
        ["OI-6", "Confirm local disk retention and cleanup behavior after observing actual alert rate.", "Project owner"],
        ["OI-7", "Define the exact five-minute dependency-stop behavior and operator restart procedure.", "Project owner"],
    ])

    heading(d, "9 Out of scope for MVP")
    bullets(d, [
        "VMware VM deployment.",
        "NAS/NFS storage and remote evidence serving.",
        "Mobile application, push notifications, and external backend.",
        "TLS, user authentication, and remote web access.",
        "Processing all six cameras simultaneously.",
        "Persistent frame replay after model failure.",
        "SCADA/PLC control or automatic equipment shutdown.",
        "Model training, model architecture, and final accuracy certification.",
    ])
    save(d, "clinker-vision-srs.docx", "SRS v0.5")


def build_sad():
    d = Document()
    title(d, [
        "SYSTEM ARCHITECTURE & DESIGN DOCUMENT",
        "Clinker Bucket Elevator Vision Monitoring System",
        "MVP: physical server and two-container deployment",
        "Document Status: Draft v0.5 — revised to match the approved MVP baseline",
    ])
    table(d, ["Version", "Change"], [["0.5", "Replaced VM/NAS/mobile assumptions with physical HP server, local disk, local web viewer, direct NVR Ethernet, and Dockerized pipeline/model." ]])

    heading(d, "1 Introduction")
    heading(d, "1.1 Purpose", 2)
    para(d, "This document defines how the MVP is structured and deployed. It covers the pipeline, model boundary, local storage, web/API viewer, concurrency, failure behavior, and Docker packaging. Model internals remain owned by the model service and are not reimplemented in the Go pipeline.")
    heading(d, "1.2 Architecture summary", 2)
    para(d, "The system is a physical-server deployment with two Docker containers: a pipeline container and a Python model container. The pipeline pulls CAM-1 from the Dahua NVR, samples full frames, calls the model over a local gRPC stream, stores alert records and evidence on local disk, and serves a local web/API viewer.")

    heading(d, "2 Architectural decisions")
    table(d, ["Decision", "MVP choice", "Reason"], [
        ["Host", "One physical HP server running Ubuntu Server LTS", "Matches available hardware and avoids unnecessary VMware complexity."],
        ["Packaging", "Docker Compose with pipeline and model containers", "Reproducible installation and an offline image bundle."],
        ["Ingestion", "FFmpeg/RTSP from NVR channel 1", "Stable RTSP handling and direct compatibility with the Dahua NVR."],
        ["Frame preparation", "Full frame only; no Go crop", "The model owns dynamic crop/resize/normalize and preserves train/serve parity."],
        ["Sampling", "One frame approximately every two seconds", "CPU-only hardware and faults develop over seconds/minutes."],
        ["Queue", "Small bounded per-camera in-memory queue, drop oldest", "Keeps the system current and avoids unlimited backlog."],
        ["Model call", "Persistent local gRPC stream per worker", "Matches the model contract while avoiding per-frame connection overhead."],
        ["Storage", "SQLite and JPEG evidence on local HDD", "No NAS is available in the MVP."],
        ["Viewer", "REST API plus local web UI in the pipeline container", "No external backend, phone app, or push service is needed."],
        ["Security", "Bind viewer to localhost; no TLS/auth in MVP", "The operator uses the server directly and the MVP is not remotely exposed."],
        ["Failure policy", "Stop after five minutes of required-dependency failure", "Avoid running in an unknown or misleading state."],
    ])

    heading(d, "3 Runtime architecture")
    code(d, "NVR CAM-1 RTSP -> pipeline: FFmpeg -> sampler -> queue -> gRPC client -> model:50051")
    code(d, "pipeline -> SQLite alerts.db + local evidence JPEGs -> localhost REST API -> localhost web viewer")
    table(d, ["Container", "Responsibilities"], [
        ["clinker-vision-pipeline", "Configuration, NVR ingestion, frame sampling, queue, gRPC client, thresholding, alert creation, overlay rendering, SQLite, retention, REST API, web UI, health logs."],
        ["clinker-vision-model", "Python/OpenCV decoding, dynamic crop, resize, normalization, inference, and response generation."],
    ])
    para(d, "The containers communicate over a private Docker Compose network. The model service is not published to the host network. The pipeline web/API port is published only on 127.0.0.1.")

    heading(d, "4 Proposed package layout")
    table(d, ["Path", "Responsibility"], [
        ["cmd/pipeline", "Process entrypoint and graceful shutdown."],
        ["internal/config", "YAML loading, validation, protected-secret checks, and defaults."],
        ["internal/ingest", "FFmpeg subprocess per enabled NVR channel."],
        ["internal/queue", "Bounded drop-oldest frame queues."],
        ["internal/inference", "gRPC stream client, correlation, timeout handling, mock client."],
        ["internal/alerting", "Thresholding and one-alert-per-detection creation."],
        ["internal/evidence", "Full-frame overlay, JPEG encoding, atomic local writes."],
        ["internal/store", "SQLite schema, retention, and local read model."],
        ["internal/web", "Local REST API and web viewer."],
        ["deploy", "Dockerfile, docker-compose.yml, example configuration, and offline packaging."],
    ])

    heading(d, "5 Concurrency and data flow")
    heading(d, "5.1 Ingestion", 2)
    para(d, "There is one ingestion loop for each enabled NVR channel. CAM-1 is enabled in the MVP. Each loop starts FFmpeg, decodes frames, samples at the configured interval, attaches frame_id/captured_at/sequence_no, and submits to the camera queue. A failure in CAM-1 is logged and enters the dependency-failure timer.")
    heading(d, "5.2 Queueing", 2)
    para(d, "The queue is bounded. A non-blocking send is used; if full, the oldest frame is discarded before the new frame is inserted. The system never attempts to process a growing historical backlog.")
    heading(d, "5.3 Inference", 2)
    para(d, "A fixed worker pool consumes frames and uses a persistent gRPC stream to the model container. Every result is correlated by frame_id. Empty detections are normal. UNAVAILABLE, timeout, malformed response, and invalid image errors are distinct logged conditions.")
    heading(d, "5.4 Alert creation", 2)
    para(d, "For each model detection meeting the configured confidence threshold, the pipeline creates one alert. The pipeline does not deduplicate repeated detections in the MVP. The evidence renderer draws every detection returned for that frame, not only the detection that caused the alert.")
    heading(d, "5.5 Shutdown", 2)
    para(d, "On a stop signal, ingestion stops first, workers finish only bounded in-flight work, SQLite is closed cleanly, and containers exit. Unprocessed frames are intentionally discarded.")

    heading(d, "6 Component design")
    heading(d, "6.1 Configuration", 2)
    bullets(d, [
        "NVR host, username, and channel URL are configuration values and must not appear in logs.",
        "CAM-1 is enabled; CAM-2 through CAM-6 are disabled.",
        "Sample interval defaults to two seconds.",
        "Confidence threshold defaults to 0.85 for the MVP.",
        "Dependency-stop timeout defaults to five minutes.",
        "Web/API binds to 127.0.0.1 only.",
    ])
    heading(d, "6.2 Evidence", 2)
    para(d, "Evidence is written to local storage using a temporary file followed by an atomic rename. A recommended path is /srv/clinker-vision/evidence/YYYY/MM/DD/CAM-1/<alert_id>.jpg. JPEG quality begins at 85. The local disk is monitored for low-space conditions.")
    heading(d, "6.3 SQLite", 2)
    para(d, "SQLite stores alerts, frame/model traceability, evidence paths, seen state, and cleanup metadata. WAL mode is enabled. The MVP does not use a NAS, external database, or external alert-delivery outbox. An alert is complete when its local DB row and evidence file are written.")
    heading(d, "6.4 Local web/API", 2)
    para(d, "The pipeline serves a read API and web UI on localhost. The model gRPC port remains internal to Docker. There is no bearer token, push notification, remote backend, or mobile client in the MVP.")

    heading(d, "7 Failure-mode design")
    table(d, ["Failure", "Handling"], [
        ["NVR unreachable", "Log the condition, mark CAM-1 unavailable, and stop after five minutes if it does not recover."],
        ["CAM-1 RTSP/decode failure", "Restart the FFmpeg subprocess with bounded retry; stop after the configured dependency timeout."],
        ["Model unavailable", "Log the condition and stop after five minutes if it does not recover. Frames are not persisted for replay."],
        ["Model timeout/invalid response", "Log with frame_id and camera_id; discard that frame and continue until the dependency timeout policy applies."],
        ["Queue saturation", "Drop oldest frames and increment a metric; never block ingestion indefinitely."],
        ["Disk full", "Stop alert/evidence processing and stop the stack after logging the reason; operator must free space or apply retention cleanup."],
        ["Pipeline crash", "Docker restart policy may restart the container; no guarantee is made for in-flight unprocessed frames."],
        ["Internet unavailable after installation", "Expected normal state; the stack must run entirely from local images and local NVR/model connections."],
    ])

    heading(d, "8 Docker deployment design")
    para(d, "The release package contains two versioned images and one Compose file. The installation runbook loads the images from local archives. The Compose file uses restart: unless-stopped for accidental process crashes, but a deliberate dependency-failure stop must not create an endless restart loop; the application should exit with a clear reason and the operator decides when to start it again.")
    code(d, "docker compose up -d")
    code(d, "docker compose ps")
    code(d, "docker compose logs --tail=200")

    heading(d, "9 Testing strategy")
    bullets(d, [
        "Unit-test queue drop-oldest behavior, thresholding, alert serialization, path generation, and bounding-box clamping.",
        "Test the pipeline with a mock model container before using the real model image.",
        "Test the CAM-1 RTSP path with a looped local recording before connecting the plant NVR.",
        "Test model timeout, model stop, NVR disconnect, malformed JPEG, disk-full behavior, and five-minute shutdown.",
        "Verify that CAM-2 through CAM-6 remain disabled.",
        "Verify that the viewer is reachable on localhost and not published to unintended interfaces.",
    ])

    heading(d, "10 Design questions closed by this revision")
    table(d, ["Question", "Decision"], [
        ["VM or physical server?", "Physical HP server."],
        ["NAS/NFS?", "Not used in MVP; local disk only."],
        ["Mobile/push?", "Deferred; local web viewer only."],
        ["TLS/auth?", "Deferred; localhost-only viewer and local Docker gRPC in MVP."],
        ["Unprocessed frames?", "Dropped; NVR retains source recording."],
        ["Deduplication?", "None in MVP."],
        ["Stop behavior?", "Stop after five minutes of required dependency failure."],
    ])

    heading(d, "11 Remaining design inputs")
    bullets(d, [
        "Exact NVR model/firmware and CAM-1 RTSP URL.",
        "Exact server CPU model and network interface arrangement.",
        "Model Docker image, proto, benchmark, and test images.",
        "Final REST response shape and web port.",
        "Measured model latency and final sample interval.",
    ])
    save(d, "clinker-vision-sad.docx", "SAD v0.5")


def build_infra():
    d = Document()
    title(d, [
        "CAMERA & SERVER INFRASTRUCTURE SPECIFICATION",
        "Clinker Bucket Elevator Vision Monitoring System",
        "MVP: direct NVR Ethernet and physical Ubuntu server",
        "Document Status: Draft v0.7 — revised to match the approved MVP baseline",
    ])
    table(d, ["Version", "Change"], [["0.7", "Replaced VMware/OT/fibre/NAS assumptions with a physical HP server, private NVR Ethernet connection, local HDD evidence, and temporary Internet only for installation." ]])

    heading(d, "1 Purpose and confirmed baseline")
    para(d, "This document records the hardware, network, storage, and installation assumptions for the MVP. Values marked TBC must be recorded during commissioning; this document must not invent IP addresses or credentials.")
    table(d, ["Item", "Current baseline", "Status"], [
        ["NVR", "Dahua network video recorder; label appears to show NVR4232-EI", "Verify exact model in web UI"],
        ["Cameras", "Six cameras connected to the NVR", "Confirmed"],
        ["MVP camera", "CAM-1 / channel 1 / godet view", "Confirmed"],
        ["Camera source", "NVR channel RTSP, not direct camera IP", "Confirmed design"],
        ["Server", "Physical HP server, approximately 2015", "Confirmed"],
        ["Memory", "16 GB RAM", "Reported; verify"],
        ["CPU", "Xeon approximately 2.40 GHz", "Exact model TBC"],
        ["Disk", "500 GB local HDD", "Reported; verify free space/health"],
        ["Operating system", "Ubuntu Server LTS", "Installed; record version"],
        ["Model location", "Same physical server in Docker", "Confirmed design"],
        ["Evidence storage", "Local server disk", "MVP decision"],
    ])

    heading(d, "2 Camera inventory")
    table(d, ["ID", "Target", "Phase", "MVP state"], [
        ["CAM-1", "Godets, line 1", "MVP", "Enabled"],
        ["CAM-2", "Godets, line 2", "Future", "Disabled"],
        ["CAM-3", "Galets, line 1", "Future", "Disabled"],
        ["CAM-4", "Galets, line 2", "Future", "Disabled"],
        ["CAM-5", "Clinker level/spillage", "Future", "Disabled"],
        ["CAM-6", "Reserve/expansion", "Future", "Disabled"],
    ])
    para(d, "The project documents currently record 2688×1520 at 25 fps, no infrared, and day/night capability for the cameras. These values should be verified from the NVR/camera configuration before performance testing.")

    heading(d, "3 Network topology")
    code(d, "CAM-1 ... CAM-6 -> Dahua NVR --private Ethernet--> Ubuntu server")
    code(d, "Ubuntu server --temporary approved Internet--> package/image installation only")
    bullets(d, [
        "The NVR is not assumed to be on the plant OT network.",
        "The NVR-to-server link should be a private or otherwise controlled network segment.",
        "The NVR IP, server IP, subnet, gateway, and interface names are deployment values and must be recorded during commissioning.",
        "Internet is required only to install Ubuntu packages and obtain Docker images, unless images are delivered by USB/offline media.",
        "After installation, disconnect or disable the temporary Internet connection.",
        "The web viewer must bind to localhost so it is usable on the server without becoming a remote service.",
    ])

    heading(d, "4 Server requirements")
    table(d, ["Resource", "MVP requirement/recommendation"], [
        ["CPU", "Xeon 2.40 GHz or better; record exact model. Benchmark the real model before accepting the sample interval."],
        ["RAM", "16 GB installed. Reserve enough memory for Ubuntu, Docker, pipeline, model, and filesystem cache."],
        ["Disk", "500 GB local HDD. Use separate directories for application data, SQLite, evidence, logs, and image archives."],
        ["OS", "Ubuntu Server LTS; keep security updates enabled during the test period."],
        ["Docker", "Docker Engine and Docker Compose plugin."],
        ["Display", "Local monitor/keyboard or approved console access for operators and maintenance."],
        ["Network", "At least one interface for the private NVR link; a temporary Internet path may use a second interface or an approved temporary change."],
    ])

    heading(d, "5 Local storage")
    table(d, ["Data", "Location", "Policy"], [
        ["SQLite alerts", "/srv/clinker-vision/data/alerts.db", "WAL mode; local only."],
        ["Evidence JPEGs", "/srv/clinker-vision/evidence/YYYY/MM/DD/CAM-1/", "Full-frame overlay; quality 85; 365-day target."],
        ["Configuration", "/srv/clinker-vision/config/", "Permissions 600; never commit credentials."],
        ["Docker images", "/srv/clinker-vision/images/", "Keep exact versioned offline archives during commissioning."],
        ["Logs", "/srv/clinker-vision/logs/ or Docker logs", "Rotate to prevent disk exhaustion."],
    ])
    para(d, "At an estimated 800 KB–1 MB per evidence image, a 500 GB disk is substantially larger than the MVP's one-year evidence requirement. Actual alert rate and free-space monitoring must still be measured. No NAS/NFS share is required for the MVP.")

    heading(d, "6 Installation checklist")
    numbered(d, [
        "Record the server hostname, Ubuntu version, exact CPU, RAM, disk health, and network interfaces.",
        "Connect the NVR and server through the approved Ethernet arrangement.",
        "Obtain the NVR IP and verify the web interface from the server.",
        "Verify CAM-1 preview and obtain the channel-1 RTSP URL.",
        "Install Docker and Docker Compose while temporary Internet is available.",
        "Load the pipeline and model images from the release package.",
        "Configure the protected NVR credentials and CAM-1 URL.",
        "Start the Compose stack and verify model, NVR, SQLite, evidence, and local web viewer.",
        "Disconnect Internet and repeat the verification.",
        "Record image tags/digests and complete the deployment sign-off.",
    ])

    heading(d, "7 Capacity and performance checks")
    table(d, ["Check", "Acceptance evidence"], [
        ["NVR connectivity", "Stable CAM-1 RTSP connection for a continuous test period."],
        ["Frame sampling", "Observed sample interval approximately two seconds."],
        ["Model throughput", "CPU benchmark from model owner and local end-to-end measurement."],
        ["Disk", "Evidence growth rate, free space, and cleanup behavior recorded."],
        ["Thermal", "Server temperature and fan behavior acceptable for the test period."],
        ["Offline operation", "Stack restarts and runs after Internet is removed."],
    ])

    heading(d, "8 Infrastructure open items")
    table(d, ["ID", "Item", "Owner"], [
        ["INF-1", "Exact NVR model, firmware, IP, credentials, codec, and CAM-1 RTSP URL.", "Project/plant"],
        ["INF-2", "Exact HP server model, CPU model, disk health, hostname, and static IP.", "Project owner"],
        ["INF-3", "Confirm whether the server has one or two network interfaces and define the temporary Internet method.", "Project/IT"],
        ["INF-4", "Run CPU/model benchmark on the real server.", "Project/model owner"],
        ["INF-5", "Confirm local disk cleanup and alert retention after real alert-rate measurement.", "Project owner"],
        ["INF-6", "Define physical access and operator console location.", "Plant/project"],
    ])
    save(d, "clinker-vision-infra.docx", "Infrastructure v0.7")


def build_security():
    d = Document()
    title(d, [
        "SECURITY & THREAT MODEL",
        "Clinker Bucket Elevator Vision Monitoring System",
        "MVP: local physical server, no remote exposure",
        "Document Status: Draft v0.2 — revised to match the approved MVP baseline",
    ])
    table(d, ["Version", "Change"], [["0.2", "Rebased security boundaries from OT/VM/NAS/mobile assumptions to the physical server, private NVR Ethernet, localhost web viewer, Docker images, and temporary installation Internet." ]])

    heading(d, "1 Security scope")
    para(d, "The MVP is deliberately local. The server, NVR, Docker containers, SQLite database, evidence images, and local web viewer are the protected assets. The viewer is opened on the server itself. No phone, Wi-Fi feed, external backend, push service, or Internet runtime dependency exists in this phase.")
    para(d, "The absence of TLS and authentication is accepted only because the web/API viewer is bound to localhost and the model service is internal to the Docker network. This decision must be revisited before any remote access is enabled.")

    heading(d, "2 Assets and trust boundaries")
    table(d, ["Asset", "Location", "Protection rule"], [
        ["NVR and camera streams", "Private NVR/camera network", "NVR credentials protected; do not expose RTSP or web admin beyond the required link."],
        ["Pipeline container", "Docker on physical Ubuntu server", "Non-root, no privileged mode, resource limits, pinned image."],
        ["Model container", "Same server, private Docker network", "Do not publish gRPC to the host; accept requests only from pipeline container."],
        ["SQLite database", "Local server disk", "Filesystem permissions restricted to application user; no remote DB access."],
        ["Evidence JPEGs", "Local server disk", "Local filesystem permissions; no remote file serving in MVP."],
        ["Configuration/secrets", "Local protected directory", "Root-owned or deployment-owner-owned with mode 600; never in images or logs."],
        ["Web/API viewer", "127.0.0.1 on server", "No remote bind, no auth/TLS in MVP by explicit scope decision."],
    ])

    heading(d, "3 Threat model")
    table(d, ["Threat", "Impact", "MVP mitigation/acceptance"], [
        ["NVR credentials leak", "Unauthorized camera/NVR access", "Store outside images/logs; restrict config permissions; rotate if exposed."],
        ["Web/API remote exposure", "Unauthenticated alert/evidence access or modification", "Bind to 127.0.0.1 only; do not use 0.0.0.0."],
        ["Docker image compromise", "Pipeline/model compromise", "Use approved versioned images, record digests, do not use untrusted images."],
        ["Container escape", "Server compromise", "Run non-root, no privileged mode, read-only filesystem where practical, resource limits."],
        ["Internet-connected installation", "Package/image supply-chain risk", "Use temporary approved access only; record image versions; disconnect after installation."],
        ["Disk failure or deletion", "Loss of alerts/evidence", "Accepted MVP risk; NVR recording is not a backup of model alerts or overlays."],
        ["Model returns invalid bbox/label", "Misleading evidence image", "Validate and clamp coordinates; escape labels; reject malformed responses."],
        ["Unauthorized physical server access", "Viewer/data access", "Physical access is the MVP authorization boundary; plant controls the room and console."],
    ])

    heading(d, "4 Network and exposure rules")
    bullets(d, [
        "The NVR-to-server Ethernet link is private/controlled and is not assumed to be OT-connected.",
        "The NVR web administration interface and RTSP service must not be exposed to the Internet.",
        "The pipeline web/API binds to 127.0.0.1 only.",
        "The model gRPC service is reachable only on the private Docker Compose network.",
        "Temporary Internet is for Ubuntu/Docker/package/image installation only.",
        "After deployment, remove or disable the Internet path and verify the stack still runs.",
        "Any future remote viewer requires a new security review, TLS, authentication, firewall rules, and updated requirements documents.",
    ])

    heading(d, "5 Authentication and secrets")
    bullets(d, [
        "There are no operator accounts or login screens in the MVP.",
        "The NVR username/password is stored only in the protected runtime configuration.",
        "Credentials must never appear in Dockerfiles, Git, screenshots, command history, or logs.",
        "The model service has no access to NVR credentials; the pipeline is the only component that constructs the NVR connection.",
        "If credentials are suspected to be exposed, stop the stack, change the NVR password, update configuration, and restart.",
    ])

    heading(d, "6 Encryption decision")
    para(d, "TLS is not used in the MVP. This applies to the local pipeline-to-model gRPC connection and the localhost web/API connection. The decision is acceptable only because neither interface is remotely exposed. TLS becomes mandatory before the viewer, model service, or NVR traffic is placed on a shared or remote network.")

    heading(d, "7 Docker and Ubuntu hardening")
    bullets(d, [
        "Use Ubuntu Server LTS and apply security updates during the installation/test period.",
        "Use pinned, versioned pipeline and model images; record image digests.",
        "Run containers as a non-root user where supported.",
        "Do not use --privileged, host networking, or unnecessary host mounts.",
        "Mount only the required config, SQLite, evidence, and log directories.",
        "Apply CPU and memory limits so an inference burst cannot make the server unusable.",
        "Keep Docker's restart policy from creating an endless loop after an intentional dependency-failure stop.",
        "Do not expose Docker's API socket to containers.",
        "Monitor local disk usage and rotate logs.",
    ])

    heading(d, "8 Data retention and backup limitation")
    para(d, "The MVP targets 365-day retention on the 500 GB local disk. No separate backup system is included. The NVR retains source camera recordings, but those recordings cannot automatically recreate the SQLite alert row, model metadata, confidence, or evidence overlay. This is an accepted MVP risk and must be shown in the deployment sign-off.")

    heading(d, "9 Logging and incident response")
    bullets(d, [
        "Log NVR connectivity, model readiness, inference latency, frame drops, alert creation, disk warnings, and stop reasons.",
        "Include camera_id and frame_id for traceability.",
        "Never log passwords, RTSP URLs containing credentials, tokens, or full image data.",
        "On repeated dependency failure, the application stops after five minutes and records the reason.",
        "On disk-full conditions, stop evidence writes and the application rather than silently losing alert data.",
        "On suspected compromise, disconnect temporary Internet, stop containers, preserve logs, rotate credentials, and involve the project owner.",
    ])

    heading(d, "10 Security sign-off conditions")
    table(d, ["Condition", "Evidence"], [
        ["Local-only web viewer", "Listening address shows 127.0.0.1, not 0.0.0.0."],
        ["No runtime Internet dependency", "Stack restarts after Internet is disconnected."],
        ["Protected credentials", "Configuration permissions are 600 and credentials are absent from logs."],
        ["Container isolation", "Model gRPC is not published to host; containers are non-root where possible."],
        ["Data limitation accepted", "Project owner signs that local data has no separate backup in MVP."],
    ])

    heading(d, "11 Deferred security work")
    bullets(d, [
        "TLS for all network interfaces.",
        "Authentication and authorization for remote viewers.",
        "Remote/mobile access and push notification security.",
        "NAS or backup security if remote storage is introduced.",
        "Formal firewall policy and network segmentation if the server leaves the private NVR link.",
        "Security review of production model/image supply chain.",
    ])
    save(d, "clinker-vision-security.docx", "Security v0.2")


if __name__ == "__main__":
    build_srs()
    build_sad()
    build_infra()
    build_security()
    print("Rebuilt SRS, SAD, Infrastructure, and Security documents")
