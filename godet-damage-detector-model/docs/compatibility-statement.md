# Compatibility statement — clinker-vision-model:final

## 1. Contract status

- Implemented: contract **v0.3** (dense-frames-in / per-godet-state-out) plus the
  four accepted follow-up blocks (slow-consumer policy, 15-case test package,
  sample artifacts, this statement).
- Source documents: `model-service-contract (2).md` (v0.2, pipeline draft),
  `model-service-contract-v0.3-proposal.md`, `model-service-implementation-plan.md`
  (all in the calibration repo; ask for copies if needed).

## 2. Protobuf compatibility: BACKWARD-INCOMPATIBLE

- Package is now **`clinkervision.inference.v2`** (`contract/inference_v2.proto`).
- v1 stubs **must** be regenerated against this file; imports repointed to v2.
- New RPC: `GetGodetState` (Tier-2). New semantics: dense ordered frames,
  measurements in `scalar_measurements`, fixed ROI indicator box, no `confidence`
  until a mapping is signed. `GodetStateResponse.events[]` carries replayable
  `damage` events with stable `event_key`, pending/confirmed state, candidate
  `evidence_frame_id`, and row-count measurements.

## 3. Required pipeline changes

1. Dense 25 fps CAM-1 lane (1 frame/camera/1–2 s sampling does NOT work here).
2. Contiguous in-order `sequence_no` per camera + gap signaling (no silent drops;
   no drop-oldest on this lane — backpressure instead).
3. Thresholds as **row counts** from the bundle (`SHORT_FRAC 0.82` →
   `short_len` L123/R112, `DROP_ROWS 25`, `SPLAY_RISE 1.0`), config-side, no
   model redeploy to retune.
4. Wire Tier-1 (`Infer`) as measurements (never operator alerts) and Tier-2
   (`GetGodetState`) as the alert channel; store `model_version` on every alert.
5. Outbox retry on `UNAVAILABLE` / `DEADLINE_EXCEEDED`; dead-letter (no retry) on
   `INVALID_ARGUMENT`. Missing Tier-1 responses are dead-lettered frames, not
   hangs — do not tear down the stream over them.

## 4. Required configuration changes

- JPEG pins: pipeline emits FFmpeg MJPEG with `-q:v 3`, full-frame output, and
  an 8 MB maximum. Validate encoder/calibration parity on release hardware
  before acceptance; changing the encoder requires recalibration.
- `--max-jpeg-bytes` (8M), `--frame-deadline-s` (5.0), `--shutdown-grace-s`,
  `MODEL_BUILD` release tag, `state/store.db` path.
- Day/night template switch rule (separate bundle per lighting; not shipped).

## 5. Required database fields (pipeline alerts DB)

- `model_version` (exact string) + dedup key (`godet_id + loop_no + rule_id`)
  on every alert row; upsert on re-delivery, never duplicate rows.
- Godet-state columns: `godet_id, state, lip, near_plate, last_seen_loop`.
- Health/status log (loop lock, resync, template/trigger alarms).

## 6. Evidence behavior

- Tier-2 pending/confirmed events carry the fixed ROI indicator box
  (`x=0.3806 y=0.1158 w=0.0536 h=0.1697` — identical on every detection, an ROI
  marker, not a localized box). Overlay it on the full-frame screenshot.
- The pipeline caches sparse Tier-1 candidate frames and correlates them by
  `evidence_frame_id`; if a candidate has aged out, it records the actual
  fallback frame used for the screenshot.
- Tier-1 responses are measurements, not evidence; normal frames store nothing.

## 7. Known limitations (do not promise past these)

- `DAMAGE` = short/bent wing lip only; no distinct `CRACK`/`MISALIGNMENT` classes.
- NEW-damage confirmation costs one full loop (**~16 min**); pending is immediate.
- Far-field ROI covers **~93%** of the chain (`near_plate` blind spot listed).
- CAM-1 night calibration only; 6-camera scale-out deferred with measured
  per-camera cost (~65 fps Tier-1 headroom per stream on dev hardware).
- Crash-consistency is at snapshot granularity (≤200-slot trailing window loss,
  same class as recording boundaries).
- Service code license TBD (all rights reserved until LICENSE is added).
