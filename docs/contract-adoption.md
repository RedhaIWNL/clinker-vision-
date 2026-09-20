# Contract adoption record

The original MVP documents describe the first v1 pipeline: sparse sampling,
confidence-based CRACK/MISALIGNMENT detections, and drop-oldest queues. The
delivered model documents define a different, blocking v0.3 proposal: dense
ordered CAM-1 frames, row-count measurements, per-godet history, and Tier-2
pending/confirmed DAMAGE state.

The integrated implementation follows the delivered model contract because the
model cannot produce the original v1 semantics:

- production input is dense 25-FPS CAM-1 with backpressure;
- production client package is `clinkervision.inference.v2`;
- Tier-1 responses are measurements only and contain no fabricated confidence;
- Tier-2 `GodetStateResponse.events[]` is the alert source;
- `DAMAGE` means short/bent wing lip under the fixed-frame row-count rules;
- CRACK, MISALIGNMENT, and the other v1 classes are not claimed by this build.

This record makes the conflict explicit. Final release acceptance requires the
pipeline owner and model owner to sign off the v2 contract and update or
supersede the older v1 source documents. Until that sign-off, this repository is
an integration candidate, not an accuracy or scope approval.
