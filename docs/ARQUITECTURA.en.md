<p align="right"><sub><b>English</b> · <a href="ARQUITECTURA.md">Español</a></sub></p>

# Architecture

> vidsquash is in progress (0.1.0): this describes how it's written, not something that has already been checked with real videos. See [PENDIENTE.en.md](PENDIENTE.en.md).

## Principles

- **Pure Go, no CGO and no external modules.** vidsquash builds into a single `.exe`; the heavy lifting is done by ffmpeg, which runs as a separate process.
- **The plan is pure.** `squash.MakePlan` takes what ffprobe said and the options, and returns the recipe or why it can't be done, without touching the disk or launching anything. That's why it's the part that has tests.
- **Validate at the boundary.** Size, times, codec and output are validated before looking for ffmpeg: a usage error doesn't wait for anything to start.
- **Never bigger than requested.** If no attempt fits the target, it's an error; "almost" isn't delivered.
- **No half-written files.** Attempts are written to a working folder of their own (in `%TEMP%`) that's deleted at the end; the best one is moved to the destination: a rename if it's on the same disk, and if not, a copy to a temporary file next to the destination that's then renamed. A crash or a Ctrl+C leaves nothing half-written in the video's folder.

## The path of a run

1. `main.go` opens the console (`tui.Open`), calls `vidsquash.Main` with the version and exits with the code it returns.
2. `validar`: that the video exists and isn't a folder; the target (`-s` or `--for`, at least 100 KB); `--from` and `--to`; the output (by default `<nombre>-<tamaño>.mp4` next to the original), that it's `.mp4`, that it isn't the input and that it doesn't exist without `--force`.
3. `ffx.Locate` looks for ffmpeg and ffprobe: next to the exe, on the `PATH` and where winget leaves them (`WinGet\Links` or the Gyan.FFmpeg package in `WinGet\Packages`).
4. `ffx.Probe` reads the duration, the size, the video track (dimensions, fps, rotation, whether it's HDR) and the audio track.
5. `squash.MakePlan` builds the recipe and the tool shows it: source, target (video + audio bits) and recipe (resolution, fps, codec, preset), with the reason for each decision.
6. `squash.Encode` runs the passes with a live bar per phase.
7. `squash.Measure` measures the quality (unless `--no-quality`), and the final card sums up size, attempts and quality.

## ffx

- It looks for ffmpeg next to the exe (portable install), then on the `PATH`, then in winget's shortcuts (`WinGet\Links`) and in the Gyan.FFmpeg package (`WinGet\Packages\Gyan.FFmpeg*\*\bin`). That last one is the case of `winget install Gyan.FFmpeg`: it adds the package's bin to the user's `PATH`, which a terminal opened before installing doesn't see, and leaves Links empty.
- It runs ffmpeg and ffprobe with cancellation: the Ctrl+C context kills the process and leaves no garbage.
- It reads the progress from `-progress pipe:1` (output time, speed, frames) and keeps the tail of stderr to explain a failure.
- It doesn't open windows of its own (`CREATE_NO_WINDOW` on Windows).

## squash: the planner

- **Budget**: the target minus a 2 % margin, minus the estimated MP4 overhead: 4,096 fixed B, 12 B per video frame and 6 B per AAC audio frame (1,024 samples at 48 kHz). What's left, divided by the duration (the stretch's if there's a cut), is the total kb/s.
- **Audio**: in steps according to the total kb/s (160, 128, 96, 64, 48 or 32 kb/s), never above the original, stereo at most (a 5.1 goes down to stereo) and mono below 64 kb/s: a clean mono sounds better than a squeezed stereo.
- **Resolution and fps**:
  - The ladder is built by the **short side** (2160, 1440, 1080, 720, 540, 480, 360, 240, starting from the original or from `--max-res`), so a vertical video isn't treated like one that's 1920 tall. It never upscales.
  - It picks the first combination of resolution and fps whose bits per pixel are enough: 0.050 for H.264, 0.035 for H.265.
  - At the same resolution it tries the original fps first and then half of it (only if the original is above 30 and the half doesn't drop below 24), with a documented 10 % tolerance in favor of keeping the resolution. `--keep-fps` takes the half out of the list.
  - If not even the last one is enough, it's used anyway, with a warning that the image will look coarse.
  - The dimensions are rounded to even numbers (yuv420p requires it).
- **Impossible targets**: below 45 kb/s of video it's no longer video, it's a mosaic; the plan returns an error with a suggestion (cut it or ask for more).

## squash: the encoding

- **Two passes**: the first one studies the whole video and notes where bits are needed; the second spreads the budget with that information.
- **Correction**: if the file goes over the target or stays below 90 %, only the second pass is repeated, because the first pass's statistics work for any bitrate. The next bitrate comes from the secant method on size(bitrate), which is monotonic: with one attempt, direct proportion on the video part; with two, the secant between the last two. It aims at 98.5 % of the target, and the last correction at 97 %, so that it fits no matter what. Three corrections at most. With safeguards for when the curve flattens (the encoder has bits to spare): it stops if raising the bitrate barely made the file bigger, it doesn't leave the interval between an attempt that fits and one that goes over, without an interval it at most doubles or halves the bitrate, and it doesn't repeat a bitrate already tried.
- It keeps the **biggest attempt that fits** the target.
- **Filters**: tone mapping if it's HDR, `scale` with lanczos if the resolution changes, `fps` if the frames change, and always `format=yuv420p`, the one anything plays.
- **HDR → SDR**: PQ or HLG to BT.709 with the Hable curve, going through linear light in floating point (`zscale`, from zimg). The chain ends in `format=yuv420p`, which forces zimg to do the conversion to YUV: without it, a `scale` right after received the floating-point RGB and swscale picked the matrix (BT.601 up to ffmpeg 6.1, in a file tagged BT.709).
- **Container**: `-movflags +faststart` (the index goes at the beginning and the video starts before it's fully downloaded, which a chat needs) and the original's metadata. In H.265 the `hvc1` tag goes in: without it, iPhone and Mac won't play it.
- `--from` goes before `-i`: when transcoding, seeking is fast and exact.

## squash: the quality

- **VMAF** (Netflix's perceptual metric, 0–100) over three 2 s windows, at 20, 50 and 80 % of the video; a video of 8 s or less is measured whole. Measuring the full file would cost another pass.
- Reference and output are brought to the same resolution (1080p at most, which is what the model is calibrated for) and to the same fps; if there was tone mapping, the reference goes through the same chain as the encoding, to compare SDR against the same SDR frames the encoder received.
- If the installed ffmpeg doesn't ship libvmaf, SSIM is used. The card turns the number into words with the usual thresholds ("indistinguishable from the original" from 93, "very good" from 85…).

## The console core

### tui

- **Live region**: each phase's progress is redrawn in place at 20 frames per second and permanent lines are printed above it, with a fixed lock order so there's no deadlock.
- **Bar** with 1/8-column resolution; in Windows Terminal the progress is also published on the icon and the tab (OSC 9;4).
- Borderless **card** and **es-AR formatting**: thousands with a period, decimals with a comma, bytes in decimal units, which are the ones services use for their limits.
- Plain text without escape codes when the output isn't a console.
- **Lines that don't fit**: errors (`✗`) are wrapped by words at the window's width, with the extra lines aligned after the mark (`Term.Marked`); into a pipe they go whole. Before, the console cut them wherever they fell.

### cli

- GNU-style flags; typed binders with validation: bounded integers, enums (`--for`, `--codec`, `--preset`), **sizes** (`25MB`, `8M`, `1,5GB`, `500KiB`, decimal and binary) and **times** (`90`, `1:30`, `01:02:03.5`, `1m30s`).
- A misspelled flag or value suggests the closest one by Damerau–Levenshtein distance (`textdist`).
- `Interrupt`: the first Ctrl+C cancels the context (ffmpeg is stopped and the temporary files are cleaned up); the second one exits immediately.

### fsx and win/desk

- `fsx`: display names and file checks (`SameFile`, `Exists`).
- `win/desk`: the console's mode and size through `syscall`. It only loads `kernel32.dll`, a KnownDLL that Windows always takes from System32 (with any other DLL named without a path, `LoadLibrary` would look next to the exe first).

### version

`Version` and `Commit` are variables that `build.ps1` overrides with `-ldflags -X`: `vidsquash --version` says `0.1.0+abc1234`. Built with `go install`, it says `0.1.0`.

<!-- i18n source=ARQUITECTURA.md sha256=68513946a907 -->
