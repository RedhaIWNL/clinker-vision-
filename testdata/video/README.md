# CAM-1 video fixture

`cam-1.mp4` is an existing NVR recording used as the Milestone 1 FFmpeg
fixture. It is plant/NVR footage rather than synthetic footage; confirm the
required plant-data approval before distributing or committing it elsewhere.

Measured with `ffprobe`:

- Duration: 1116 seconds (18 minutes 36 seconds)
- Resolution: 2688x1520
- Frame rate: 25 FPS
- Codec: HEVC/H.265
- Source: NVR channel 1 recording

The fixture is used only to exercise ingestion and decoding. It must not be
used to claim model accuracy or production detection performance.
