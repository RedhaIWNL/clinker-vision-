"""Canned responder (deliverable artifact for pipeline integration testing).

Deterministic: identical input bytes always produce the identical response.
No CV, no state, no randomness. model_version is the fixed string "mock-0".
"""
from __future__ import annotations

import hashlib
from datetime import datetime, timezone

MODEL_VERSION = "mock-0"

# Fixed ROI indicator for 2688x1520 CAM-1 night ROI. Identical on every detection.
FIXED_BOX = {"x": 0.3806, "y": 0.1158, "width": 0.0536, "height": 0.1697}

# status_code enum (contract-pinned): readable=0, weak=1, edge=2, occluded=3.
MOCK_SCALARS = {
    "lit_L": 140.0,
    "lit_R": 135.0,
    "peak": 0.9,
    "dx": 0.0,
    "dy": 0.0,
    "status_code": 0.0,
}

LOOP_SLOTS = 1208  # chain loop length in slots (PLAN_v3 §5); mock slot is illustrative.


def mock_scalars(image_data: bytes) -> dict:
    """Deterministic canned scalars. slot derives from the frame hash so repeated
    delivery of the same frame is byte-identical (idempotency-friendly)."""
    h = int(hashlib.sha256(image_data).hexdigest(), 16)
    out = dict(MOCK_SCALARS)
    out["slot"] = float(h % LOOP_SLOTS)
    return out


def utc_now_pb2():
    from google.protobuf.timestamp_pb2 import Timestamp

    t = Timestamp()
    t.FromDatetime(datetime.now(timezone.utc))
    return t


def build_mock_response(pb2, frame_id: str, image_data: bytes):
    """A Tier-1-shaped mock response: fixed-box GODET/DAMAGE detection + scalars."""
    resp = pb2.InferenceResponse()
    resp.frame_id = frame_id
    det = resp.detections.add()
    det.observation_target = "GODET"
    det.fault_type = "DAMAGE"
    det.bounding_box.x = FIXED_BOX["x"]
    det.bounding_box.y = FIXED_BOX["y"]
    det.bounding_box.width = FIXED_BOX["width"]
    det.bounding_box.height = FIXED_BOX["height"]
    for k, v in mock_scalars(image_data).items():
        resp.scalar_measurements[k] = v
    resp.model_version = MODEL_VERSION
    resp.processed_at.CopyFrom(utc_now_pb2())
    return resp


def build_mock_state(pb2):
    """Mock Tier-2 state: no detector, so not ready — explicit, never fake data."""
    out = pb2.GodetStateResponse()
    out.model_version = MODEL_VERSION
    out.health.ready = False
    out.health.loop_locked = False
    out.health.status = "not_ready"
    out.health.detail = "mock mode: no detector state"
    return out
