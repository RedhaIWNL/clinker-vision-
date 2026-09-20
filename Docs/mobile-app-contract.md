# MVP Local Web/API Contract — Clinker Vision

**Version:** v0.3  
**Status:** MVP contract; mobile application and remote backend are deferred.  
**Deployment:** The pipeline and local viewer run on one physical Ubuntu server.  
**Access:** The operator opens the viewer directly on that server. No authentication or TLS is used in the localhost-only MVP.

> This file keeps its historical filename for compatibility. It no longer defines a mobile-app or push-notification dependency.

## 1. MVP responsibilities

- The pipeline reads model detections, applies the configured confidence threshold, creates alert rows, writes evidence JPEGs, and serves the local API.
- The local web viewer reads the API and displays alert metadata and evidence images.
- SQLite and evidence files remain on the server's local disk.
- There is no external backend, mobile application, push notification, bearer token, NAS, NFS, or remote evidence URL in the MVP.

## 2. Local transport and access

The API and web viewer are served over HTTP on `127.0.0.1`. The model gRPC service is internal to the Docker Compose network and is not published to the host.

The MVP deliberately has:

- No operator accounts.
- No login page.
- No remote browser access.
- No TLS.
- No mobile push notifications.

These choices are acceptable only while the viewer remains local to the server. Any future remote access requires authentication, TLS, firewall rules, and a security review.

## 3. List alerts

```http
GET /api/v1/alerts?camera_id=CAM-1&fault_type=CRACK&unseen_only=true&limit=50&cursor=<opaque>
```

The response is JSON sorted by `detected_at` descending. Supported filters should include:

- `camera_id`
- `fault_type`
- `unseen_only`
- `since`
- `limit`
- `cursor`, if cursor pagination is implemented

Example response:

```json
{
  "items": [
    {
      "alert_id": "3f9c2b8a-1111-4444-aaaa-bbbbbbbbbbbb",
      "camera_id": "CAM-1",
      "observation_target": "godet",
      "fault_type": "MISALIGNMENT",
      "confidence": 0.91,
      "captured_at": "2026-09-17T14:03:22+01:00",
      "detected_at": "2026-09-17T14:03:22+01:00",
      "frame_id": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
      "model_version": "yolo-clinker-2026.09.1",
      "bbox": {"x": 0.21, "y": 0.34, "w": 0.18, "h": 0.12},
      "evidence_ref": "2026/09/17/CAM-1/3f9c2b8a-1111-4444-aaaa-bbbbbbbbbbbb.jpg",
      "evidence_url": "/api/v1/alerts/3f9c2b8a-1111-4444-aaaa-bbbbbbbbbbbb/evidence",
      "seen_at": null
    }
  ],
  "next_cursor": null
}
```

## 4. Read one alert and evidence

```http
GET /api/v1/alerts/{alert_id}
GET /api/v1/alerts/{alert_id}/evidence
```

The evidence endpoint must serve only the JPEG associated with the alert. The implementation must prevent a requested path from escaping `/srv/clinker-vision/evidence`.

Evidence rules:

- One JPEG per alert.
- Full frame, not a crop.
- All detections from the frame overlaid.
- Red boxes and fault/confidence labels.
- JPEG quality starts at 85.
- Written using a temporary file and atomic rename.

## 5. Mark an alert seen

```http
POST /api/v1/alerts/{alert_id}/seen
```

Response:

```json
{
  "alert_id": "3f9c2b8a-1111-4444-aaaa-bbbbbbbbbbbb",
  "seen_at": "2026-09-17T14:05:01+01:00"
}
```

The first request sets the shared `seen_at` value. Repeated requests are no-ops. There is no user or device identity in this phase.

## 6. Alert creation rules

The pipeline creates one local alert for each model detection whose raw confidence meets the configured threshold. The MVP does not deduplicate repeated detections or apply a cooldown.

There is no `POST /v1/alerts` endpoint in the MVP because the pipeline writes directly to its own SQLite database. A future backend contract may introduce that endpoint if mobile/remote delivery is approved.

## 7. Deferred mobile contract

Mobile access, push notifications, remote evidence serving, and external backend ownership are future work. When that work begins, define a new versioned contract covering authentication, TLS, device registration, notification delivery, remote image serving, per-device seen state, and offline behavior. Do not build the MVP against the old backend/push assumptions.
