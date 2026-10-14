<p align="right"><sub><b>English</b> · <a href="VERIFICACION.md">Español</a></sub></p>

# Verification

vidsquash is in progress (0.1.0). This page says what's checked and what isn't, without rounding up.

## What's checked

### Go tests

```powershell
go test ./...
.\build.ps1 -Test     # go vet + go test and then builds
```

69 tests in `cli`, `ffx`, `fsx`, `textdist`, `tui`, `win/desk` and `squash`. The 7 for the planner (`go test ./internal/squash/`) check, on described videos (without ffmpeg):

- the plan never promises more bytes than the target: video + audio + container ≤ target;
- with few bits per pixel, the fps drops first (60 → 30) and then the resolution;
- vertical videos are scaled by the short side;
- a small video is never upscaled, and with bits to spare everything is kept;
- an impossible target gives `ErrTooSmall` with a suggestion, not a broken plan;
- the cut is respected;
- the audio rules: with few bits it goes mono and never exceeds the original.

For `ffx`, it's tested that it finds ffmpeg in the package left by `winget install Gyan.FFmpeg` (a `LOCALAPPDATA` built in a temporary folder); reading the video, encoding and measuring are tested by the HDR tests below, which need ffmpeg.

There's also a dependency test: `go list -deps` on `tui` can't pull in `net`, `net/netip` or `os/exec` (vidsquash does launch processes, but that lives in `ffx`).

### Size correction

Seven tests guard the corrected second pass: the proportion with one attempt, the ×2 and ×½ cap per correction, the regula falsi between an attempt that fits and one that goes over (bisection if noise flips the slope), that a bitrate already tried isn't repeated, and the real case that broke `Encode`: a 2 s clip with a 2 MB target, where x264 had nothing left to spend bits on, the size didn't move and the secant asked for 28,620 Mb/s and then 117 million, until libx264 wouldn't open. Now it stops and delivers what fits (with ffmpeg: 2 second passes, 321 KB).

The full loop is tested against simulated encoders (exact, one that overshoots by 8 %, one that falls 25 % short, with ±4 % noise, one that saturates around 60 % and one with a hard ceiling at 40 %) in 9 combinations of target and duration each: 54 cases. In all of them, no correction more than doubles the highest bitrate tried and there are never more than 4 second passes; if the target is reachable it ends between 90 and 100 %, and if the encoder saturates first it delivers something that fits with at most two corrections.

### HDR: tone mapping and measurement

Five tests. The ones that run ffmpeg generate their originals (testsrc2 brought to BT.2020 with PQ or with HLG, in 10-bit ProRes inside a `.mov`, and a vertical one with rotation, the way a phone records it) and are skipped, saying so, if ffmpeg isn't there or doesn't ship zimg: they don't run on GitHub's runner.

- In every chain with tone mapping (encoding unchanged, downscaling, at half the fps and the measurement's reference), the `zscale` that applies the BT.709 matrix is followed by a fixed `format=`.
- An SDR original doesn't go through tone mapping, and in the measurement the tone mapping is applied only on the original's side.
- Without a size change, the measurement's reference is bit-for-bit identical (PSNR ∞) to what the encoder receives: PQ, HLG and rotated vertical.
- When downscaling, what the encoder receives is in BT.709, as the file's tag says: it's compared against the same original converted without downscaling and shrunk with zimg.
- End to end (`ffx.Probe`, the plan, the two passes and the measurement), with winget's ffmpeg:

| original | output | SSIM | VMAF |
|---|---|---|---|
| PQ 640×360 | 640×360 · 570 KB | 0.9992 | 98.2 |
| HLG 640×360 | 426×240 · 75 KB | 0.9791 | 88.1 |
| rotated vertical HLG | 360×640 · 569 KB | 0.9992 | 98.1 |

**The bug they close.** The last `zscale` in the tone mapping negotiated its output format with the next filter. If that was a `scale` (when downscaling, and always in the measurement), zscale handed over its floating-point working RGB, the `m=bt709:r=tv` wasn't applied and the conversion to YUV was done by swscale: with BT.601 up to ffmpeg 6.1, in a file tagged BT.709. The measurement compared the output against a different conversion of the original than the one the encoder had received. Now the chain ends in `format=yuv420p` and the conversion is always done by zimg. Without downscaling, the encoder receives exactly the same frames as before (identical framemd5); when downscaling a 4K HLG, against the ideal floating-point conversion, it goes from 47.5 to 50.7 dB with ffmpeg 8.1 and from 29.2 to 47.4 dB with 6.1, and filtering is ~10 % faster.

The version matrix runs these tests with each ffmpeg:

```powershell
python internal\squash\_oracle\versiones.py <carpeta de ffmpeg> [<carpeta> ...]
```

| ffmpeg | reference against encoder, before | after | downscaled in BT.709, before | after |
|---|---|---|---|---|
| 5.1.3 and 6.1.1 | 29.6 dB | ∞ | 29.6 dB (BT.601) | 54.4 dB |
| 7.0.2 | 40.6 dB | ∞ | 40.5 dB | 54.4 dB |
| 7.1.1, 8.0.1, 8.1.1 and 9.0.2 | 47 to 49 dB | ∞ | 45.0 dB | 54.4 dB |

After the fix, all seven versions give the same result in every case. 5.1.3 (UiPath's) skips the end-to-end test because it doesn't ship libx264, and the vertical case because it doesn't have `-display_rotation` to write the rotation.

### The exe's boundary, on every CI run

[`.github/frontera.ps1`](../.github/frontera.ps1) runs the `.exe` in a temporary folder with a one-byte `clip.mp4` (no validation opens it) and checks the message and the code of each case in the table below, plus a misspelled flag (`--presset`, code 2). The "ffmpeg not found" one only runs if ffmpeg isn't where vidsquash looks for it (on GitHub's runner it isn't); if it is, it's skipped and says so. The CI runs it on every push, after `build.ps1` ([`ci.yml`](../.github/workflows/ci.yml)).

The CI hadn't run on GitHub yet (the repo hadn't been published): it was validated with actionlint and simulated on the development PC, with a clean clone, Go 1.26.0 and empty caches. Every step green. The boundary, 9 out of 9 with an empty `LOCALAPPDATA` (like the runner, with no ffmpeg in sight); on the PC, which has winget's ffmpeg, 8 out of 8 with the ffmpeg case skipped and reported.

### The exe, on a machine without ffmpeg (Sep 28, 2026)

With the `vidsquash.exe` this repo builds, on Windows 11 with Go 1.26.4 (the messages are the tool's, in Spanish):

| What was run | What it gave |
|---|---|
| `vidsquash --help`, `vidsquash --version` | the full help and `vidsquash 0.1.0`, code 0 |
| a file that doesn't exist | "noexiste.mp4 no existe", code 3 |
| a file without `-s` or `--for` | "falta el tamaño objetivo: -s 25MB o --for discord", code 3 |
| `-s 50KB` | "50,0 KB es demasiado poco para un video", code 3 |
| `--from 1:00 --to 0:30` | "--to tiene que ser posterior a --from", code 3 |
| `--for discrod` | suggests "discord", code 2 |
| a valid file without ffmpeg installed | "no encontré ffmpeg (instalalo con: winget install Gyan.FFmpeg)", code 3 |

That first run of the exe found a raw message: a nonexistent file gave Windows' error in English (`GetFileAttributesEx … The system cannot find the file specified.`); now it says "no existe", like the other validations.

## What's left to check

Everything that happens after finding ffmpeg, with real videos. Reading the video, encoding, correcting the size and measuring the quality already run end to end in the tests, but on 2 s synthetic originals: **they have never run against a real video.** The missing test, with `ffprobe` as the oracle (independent of vidsquash), is in [PENDIENTE.en.md](PENDIENTE.en.md): in every case, the final size has to be less than or equal to the target, and the duration and the tracks have to be preserved.

**Speed references** measured on the development PC when the engine was written: x264 `medium` at 720p30 ≈ 1.5 times real time; VMAF over 20 s of 1080p60 ≈ 22 s. That's why the quality is measured on samples and not on the whole video.

<!-- i18n source=VERIFICACION.md sha256=a751c74c6ee7 -->
