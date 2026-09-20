# Sample clip manifest

Bulk video is NOT committed (hundreds of MB). Everything below reproduces
byte-identically from the NVR archive with the commands given.

Source file (cam repo `data/`): `camera_1/night/NVR_ch1_main_20260820000000_20260820010000.mp4`
(hour 00:00–01:00, 2688×1520 @ 25 fps; frame N = second N/25 of the hour).

| Artifact | Time in hour | Frames | Command |
|---|---|---|---|
| Labelled window slice (damage + plates) | 1500–1590 s | 37500–39750 | `ffmpeg -ss 1500 -i <src> -t 90 -c copy clip_1500_1590.mp4` |
| `frames/healthy.jpg` | 319.44 s | 7986 | single-frame extract, JPEG q95 |
| `frames/damage.jpg` | 109.64 s | 2741 | single-frame extract, JPEG q95 |
| `frames/plate.jpg` | 153.76 s | 3844 | single-frame extract, JPEG q95 |

Single frame: `ffmpeg -ss <seconds> -i <src> -frames:v 1 -q:v 2 frame.jpg`
then re-encode at quality 95 through OpenCV to match serving input
(`tests/support.py:encode`, `test_fixtures.py` asserts ±2-row agreement).
