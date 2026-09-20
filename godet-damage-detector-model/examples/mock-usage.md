# Mock responder usage (pipeline integration)

Start the canned responder (deterministic, zero CV, `model_version: mock-0`):

```bash
docker run --rm -p 50051:50051 clinker-vision-model:mock
# or without docker:
python -m src.server --bind 0.0.0.0:50051 --mock
```

Send `InferenceRequest` frames (`camera_id: CAM-1`, valid JPEG bytes, contiguous
`sequence_no`). Every valid frame gets one `InferenceResponse`: `frame_id` echoed,
one fixed-box `GODET/DAMAGE` detection, canned scalars, `processed_at` set.

Invalid frames (bad JPEG, unknown camera, oversized) get **no response** and are
dead-lettered server-side (log + counter) — assert your client tolerates missing
responses without tearing down the stream.

Health: standard gRPC health, `SERVING` in mock mode.
