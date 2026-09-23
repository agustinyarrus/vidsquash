package squash

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/agustinyarrus/vidsquash/internal/ffx"
)

// quality.go mide qué tan parecido quedó el video al original. Se usa VMAF (la
// métrica perceptual de Netflix, 0–100) sobre tres ventanas cortas repartidas
// en el video: medir el archivo entero costaría otra pasada completa, y tres
// muestras de dos segundos ya dicen si la compresión se nota. Si el ffmpeg
// instalado no trae libvmaf, se cae a SSIM.

const (
	sampleWindow   = 2 * time.Second
	shortVideo     = 8 * time.Second // por debajo, se mide el video entero
	vmafReferenceH = 1080            // el modelo estándar de VMAF está calibrado para 1080p
)

var (
	vmafScore = regexp.MustCompile(`VMAF score:\s*([0-9.]+)`)
	ssimScore = regexp.MustCompile(`All:\s*([0-9.]+)`)
)

// Quality es el resultado de la medición.
type Quality struct {
	Metric  string // "VMAF" o "SSIM"
	Score   float64
	Samples int
}

// Label traduce el número a palabras, con los umbrales usuales de VMAF.
func (q Quality) Label() string {
	s := q.Score
	if q.Metric == "SSIM" {
		s = (q.Score - 0.80) / 0.20 * 100 // escala aproximada para describir
	}
	switch {
	case s >= 93:
		return "indistinguible del original"
	case s >= 85:
		return "muy buena"
	case s >= 75:
		return "buena"
	case s >= 60:
		return "aceptable"
	}
	return "se nota la compresión"
}

// sampleStarts reparte las ventanas de medición (en tiempo de la SALIDA).
func sampleStarts(dur time.Duration) []time.Duration {
	if dur <= shortVideo {
		return []time.Duration{0}
	}
	var out []time.Duration
	for _, f := range []float64{0.2, 0.5, 0.8} {
		t := time.Duration(float64(dur) * f)
		if t+sampleWindow > dur {
			t = dur - sampleWindow
		}
		out = append(out, t)
	}
	return out
}

// Measure compara la salida con el original en las ventanas de muestra.
func Measure(ctx context.Context, tools ffx.Tools, p Plan, input string, from time.Duration, output string, v *ffx.VideoStream) (Quality, error) {
	refW, refH := v.Width, v.Height
	if short := min(refW, refH); short > vmafReferenceH {
		refW, refH = scaledSize(v.Width, v.Height, vmafReferenceH)
	}
	win := sampleWindow
	if p.Duration <= shortVideo {
		win = p.Duration
	}
	threads := strconv.Itoa(max(1, runtime.NumCPU()))
	q := Quality{Metric: "VMAF"}
	var total float64
	for _, t := range sampleStarts(p.Duration) {
		score, metric, err := measureWindow(ctx, tools, p, input, from+t, output, t, win, refW, refH, v.FPS, threads, q.Metric)
		if err != nil {
			return Quality{}, err
		}
		q.Metric = metric
		total += score
		q.Samples++
	}
	q.Score = total / float64(q.Samples)
	return q, nil
}

func measureWindow(ctx context.Context, tools ffx.Tools, p Plan, input string, refAt time.Duration, output string, outAt, win time.Duration, w, h int, fps float64, threads, metric string) (float64, string, error) {
	fpsStr := strconv.FormatFloat(fps, 'f', -1, 64)
	norm := fmt.Sprintf("scale=%d:%d:flags=bicubic,fps=%s,setpts=PTS-STARTPTS", w, h, fpsStr)
	ref := norm
	if p.Tonemap {
		ref = strings.Join(tonemapChain, ",") + "," + norm // comparar SDR contra SDR
	}
	build := func(filter string) []string {
		return []string{
			"-ss", secs(outAt), "-t", secs(win), "-i", output,
			"-ss", secs(refAt), "-t", secs(win), "-i", input,
			"-lavfi", fmt.Sprintf("[0:v]%s,format=yuv420p[d];[1:v]%s,format=yuv420p[r];[d][r]%s", norm, ref, filter),
			"-f", "null", "-",
		}
	}
	if metric == "VMAF" {
		out, err := tools.Output(ctx, build("libvmaf=n_subsample=2:n_threads="+threads))
		if m := vmafScore.FindStringSubmatch(out); err == nil && m != nil {
			s, _ := strconv.ParseFloat(m[1], 64)
			return s, "VMAF", nil
		}
		if ctx.Err() != nil {
			return 0, "", ctx.Err()
		}
		// Sin libvmaf en este ffmpeg: SSIM, que viene en cualquier compilación.
	}
	out, err := tools.Output(ctx, build("ssim"))
	if err != nil {
		return 0, "", err
	}
	m := ssimScore.FindStringSubmatch(out)
	if m == nil {
		return 0, "", fmt.Errorf("ffmpeg no devolvió el puntaje de calidad")
	}
	s, _ := strconv.ParseFloat(m[1], 64)
	return s, "SSIM", nil
}
