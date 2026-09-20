# Recalibration runbook

Recalibrate (rebuild the bundle) when ANY of these happens — the service tells
you which, but do not wait for it if you already know:

| Signal | Meaning |
|---|---|
| `template_lost` health | Camera moved, swapped, or ROI changed; or wrong feed entirely |
| `LOOP DRIFT` / `chain_changed` | Chain length changed (godets added/removed) → master rebuild |
| `population_alarm` that persists | Light or calibration shifted, not damage |
| Known plant event | Maintenance touched the camera; day/night season change; JPEG settings changed |

## Procedure (CAM-1 night shown; repeat per camera)

1. Record one full hour of the new view (needs ≥2 chain loops ≈ 35 min minimum).
2. In the `cam` repo (conda env `cv`):
   ```bash
   python -m godet3.decode --video <hour>.mp4 --out out/recal
   python -m godet3.run_all --hour out/recal --labels out/review
   python -m godet3.compare --a out/h00 --b out/recal   # hold-out gates green
   ```
   All step checks must print PASS. If the template shows anything but two clean
   bars, stop — the view itself is wrong, not the calibration.
3. Build the bundle (byte-deterministic; run twice and diff to prove it):
   ```bash
   python -m godet3.bundle --hour out/recal \
       --config config/camera_1_night.json --out clinker-vision-model/model
   ```
4. Thresholds unchanged → keep `thresholds_id`; any rule value changed (see
   `godet3/common.py RULES`) → bump `THRESHOLDS_ID` (new history era; old
   snapshots will NOT restore — by design).
5. Deploy `model/` (3 files + CHECKSUMS) with the service; set `MODEL_BUILD` to the
   release tag so `model_version` traces it.
6. Restart the service; watch health: `not_ready` (~20 min first alignment) →
   `SERVING` on loop lock. Any `template_lost` in the first hour → roll back to
   the previous bundle (keep the last two bundles deployed side by side).

## Never

- Never hand-edit `model/` files (checksums fail closed on purpose).
- Never reuse a night bundle on day footage (separate template per lighting).
- Never "fix" a `population_alarm` by widening thresholds without new labels.
