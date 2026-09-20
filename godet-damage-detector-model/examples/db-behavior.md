# Expected database / output behavior

## Tier-1 (`Infer` response, per frame)

No rows anywhere. A response is emitted for every valid frame; the pipeline
stores nothing for it (alerts-only downstream). Missing response = the frame
was dead-lettered (bad JPEG / unknown camera / oversized / over deadline);
server logs carry `frame_id` + reason, counters carry the counts.

## Tier-2 events (pending / confirmed)

Emitted through `GodetStateResponse.events[]` via `GetGodetState` polling. Each
event has a stable dedup key (`DAMAGE:<godet_id>:<loop>`), candidate frame ID,
state, and measurements. Shape (`examples/responses/pending-224.json`):

```json
{"kind": "damage", "godet_id": 224, "loop": 3, "state": "pending",
 "payload": {"lip_before": 126.0, "lip_now": 79.0, "drop": 47.0},
 "model_version": "498184ea+dev+thr-v1"}
```

`confirmed` replaces `pending` for the same key when the next loop confirms
(same key → pipeline upserts, never duplicates).

## Snapshot store (service-side SQLite, `state/store.db`)

- `godet_rows(loop, slot, godet_id, payload)` — every finalized godet row,
  upsert by `(loop, slot)`. This is the restart-recovery source of truth.
- `alert_keys(kind, godet_id, loop)` — every emitted pending/confirmed key.
- `loops(loop_no, start_slot, score, margin)` + `kv` (version, identity JSON,
  loop starts, unfinalized tail).
- Normal frames and healthy godets write **godet rows only** — no alert keys,
  no events. Nothing qualifying ⇒ `detections: []`, no alert row, nothing
  downstream.

## Health (`GetGodetState.health` + gRPC health)

No rows. `SERVING` ⇔ loop locked; before that `NOT_SERVING` + `not_ready`
(`template_lost` overrides to loud failure when the feed is wrong).
