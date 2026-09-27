# CAM-4 (day side plates) in the model service

CAM-4 watches the outside side plates of the godet chain by day. The fault it looks for is a
plate that does not tuck under its neighbour: its clinker-side edge rides high as a lip with a
dark gap under it. "Gap" and "misplaced" are the same fault at different strengths.

It is a separate pipeline next to CAM-1. It has its own ordering lane, bundle
(`model/cam4/`) and history. CAM-1 behaviour does not change.

## How it works

1. **Odometer.** Each frame is matched to the previous 1–4 frames on a plate-only patch
   (phase correlation along the 115° travel direction). This gives the chain movement per
   frame: about 8.1 px at normal speed, 0 when the conveyor stops.
2. **Unrolled chain.** A thin slit across the plate band is pasted at the chain position,
   building one long picture of the chain. In it, every plate is seen from the same place.
3. **Detector.** Per column: height of the raised edge and length of the closed-in dark gap
   under it. The score is shadow/4 + 3 × raised, with pole and clinker jumps rejected
   (`detector` block of `calibration.json`).
4. **Identity.** Pieces of the chain are matched to the bundle's chain map (one loop =
   194,778 columns = 1,210 godets). After lock, the odometer predicts where the next piece
   lands, and a small search confirms it. A pass is one godet crossing the slit.
5. **History.** Light changes the score level, so each pass is scored relative to the last
   loop of passes: (raw − median) / (p90 − median).
   - A godet is **confirmed** when the median of its recent relative scores is ≥ 1.65 over at
     least 2 passes.
   - It is **suspect** when only its latest pass is ≥ 1.65.
   - Only confirmed godets become alerts.

## Contract (additive to v0.3)

| Where | CAM-4 content |
|---|---|
| `InferenceRequest.camera_id` | `CAM-4` (own `sequence_no` series; a fall-back restart is accepted) |
| Tier-1 `scalar_measurements` | `chain_step`, `chain_pos`, `match_quality`, `chain_status` (0 moving, 1 weak view, 2 stopped) |
| Tier-1 `detections` | always empty |
| Tier-1 `retained_frames` | earlier frames with a strong fault spot (the JPEG as sent), at most 2 per response |
| `GetGodetState(camera_id="CAM-4")` | CAM-4 godets (`severity`, `severity_history`, `passes_seen`), CAM-4 health, and one event per confirmed godet |
| `GodetAlertEvent` | key `CAM-4:DAMAGE:<godet>:<loop>`, `evidence_frame_id`, `evidence_box` (fault spot, normalized), measurements `severity`, `passes_seen`, `evidence_loop` |

Godet ids are 1–1210. The chain map's godet 0 is reported as 1210, the same godet one loop
on. All other numbers match the review pages.

**Evidence.** A spot is only known once its columns are scored: the detector needs chain on
both sides, so this happens about 10 s after the frame.
- The engine keeps recent JPEGs, one per 4 px of chain position. A stopped conveyor does not
  fill the buffer. The buffer holds about 270 frames at normal speed and is capped at 900.
- When a half-plate block scores above 75 % of the pass alarm, its frame is handed back in
  `retained_frames`, and the pipeline caches it.
- A godet's evidence is its latest strong pass, so the frame is recent enough to still be in
  the pipeline's cache.

**Restart.** History, loop count and the running scale are saved in the SQLite store under
`cam4_state`: every 200 passes, and at shutdown. After a restart, identity re-locks on the
chain map in about 1 minute.

## Checks

- `tests/test_cam4.py`: bundle checksum, sequencing and restart, ids and event keys,
  evidence box, persistence, server wiring with both cameras on one stream, and 600 real
  frames (the odometer matches batch 8.08 px/frame).
- `tools/cam4_replay.py`: full-hour replay through the engine with pipeline-identical JPEGs
  (`python tools/cam4_replay.py --out <dir> <video.mp4> ...`).

## Limits

- Calibrated on 2026-09-07 10:00 and 12:00 light only. The thresholds were chosen on those
  grades, so the first live days are the real test.
- Colour is not used, because the pink/green blotches are unreliable.
- The chain map must be rebuilt if the chain is changed (plates replaced or reordered), or
  if the camera moves. Health then shows `lost the chain map` or `VIEW LOST`.
