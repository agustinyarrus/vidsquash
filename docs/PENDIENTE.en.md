<p align="right"><sub><b>English</b> · <a href="PENDIENTE.md">Español</a></sub></p>

# To do

What's missing, in the order it's best done. Each point says what's there today, what's missing and how it's verified.

## 1. The end-to-end test with real videos

**Today it's written and it builds:**

- `internal/ffx`: finding ffmpeg/ffprobe, reading the video's properties (including the rotation and whether it's HDR), running ffmpeg while reading the progress from `-progress`.
- `internal/squash`: the planner (7 tests), the two-pass encoding with size correction by the secant method (only the second pass is redone), the HDR → SDR conversion and quality measurement with VMAF over three samples.
- `internal/vidsquash` and `main.go`: the complete interface (flags, service presets, live progress, final card), tested without ffmpeg ([VERIFICACION.en.md](VERIFICACION.en.md)).

**It still has to be run against real videos** (with `winget install Gyan.FFmpeg`):

- a synthetic 1080p60 clip with audio (`ffmpeg -f lavfi -i testsrc2=s=1920x1080:r=60:d=20 -f lavfi -i sine=d=20 -shortest clip.mp4`);
- a real phone video, vertical;
- an iPhone HDR video (HLG) with rotation;
- targets of 10, 16 and 25 MB on each one, and a cut with `--from`/`--to`.

In all of them, `ffprobe` has to confirm that the size is less than or equal to the target and that the duration and the tracks are preserved; in the vertical and the rotated ones, that the output's orientation is the one you see on the phone. What's worth looking at in that first run, because no test covers it:

- that the rotation is applied only once (ffmpeg only rotates when decoding; if the rotation metadata is copied too, the video would come out rotated twice);
- a video with cover art (an image attached as the first video track): `-map 0:v:0` could take the image instead of the video;
- a video with no declared fps or with variable fps;
- the tone mapping chain with a real iPhone HDR video (Dolby Vision 8.4, variable fps): with synthetic originals it's already tested, with winget's ffmpeg and six more versions ([VERIFICACION.en.md](VERIFICACION.en.md#hdr-tone-mapping-and-measurement));
- that the correction converges in one or two attempts, as the design says (against simulated encoders it's already tested in 54 cases, and the saturated case with ffmpeg).

With that run passed, vidsquash reaches 1.0.0.

## 2. `--analyze`

Picking resolution and fps by measuring (encoding samples of each candidate and keeping the one with the best VMAF) instead of only by bits per pixel. It's designed, not written.

## 3. Minor ones

- A missing target size is a usage error and today it exits with code 3; it should be 2, like the other command-line errors.
- When the output goes to a different disk than `%TEMP%`, the final move retries the rename five times (~1.2 s) before copying: `ERROR_NOT_SAME_DEVICE` could be recognized and the copy done directly.
- Tests for `ffx` with a fake ffprobe (a test exe that answers with the JSON of recorded cases), to cover reading rotation and HDR without ffmpeg.

## Distribution

- Done: the CI ([`ci.yml`](../.github/workflows/ci.yml)) runs the tests and the `.exe`'s boundary ([`frontera.ps1`](../.github/frontera.ps1)) on every push.
- Missing: with the test from point 1, add the synthetic clip to the CI (ffmpeg on the runner, like img's CI), and only then the 1.0.0 release, with `vidsquash.exe`, its SHA256 and the same tagging and reproducibility scheme as the other tools.

<!-- i18n source=PENDIENTE.md sha256=002041cc4f88 -->
