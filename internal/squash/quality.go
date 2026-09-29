package squash

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
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

	metricVMAF = "VMAF"
	metricSSIM = "SSIM"
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
	if q.Metric == metricSSIM {
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

// Measure compara la salida con el original en las ventanas de muestra: con
// VMAF, o con SSIM si este ffmpeg no trae libvmaf.
func Measure(ctx context.Context, tools ffx.Tools, p Plan, input string, from time.Duration, output string, v *ffx.VideoStream) (Quality, error) {
	return measure(ctx, tools, p, input, from, output, v, metricVMAF)
}

// measure es Measure empezando por la métrica pedida (los tests piden SSIM, la
// que da en cualquier compilación de ffmpeg).
func measure(ctx context.Context, tools ffx.Tools, p Plan, input string, from time.Duration, output string, v *ffx.VideoStream, metric string) (Quality, error) {
	refW, refH := v.Width, v.Height
	if short := min(refW, refH); short > vmafReferenceH {
		refW, refH = scaledSize(v.Width, v.Height, vmafReferenceH)
	}
	win := sampleWindow
	if p.Duration <= shortVideo {
		win = p.Duration
	}
	threads := strconv.Itoa(max(1, runtime.NumCPU()))
	q := Quality{Metric: metric}
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
	build := func(filter string) []string {
		return []string{
			"-ss", secs(outAt), "-t", secs(win), "-i", output,
			"-ss", secs(refAt), "-t", secs(win), "-i", input,
			"-lavfi", compareGraph(p, w, h, fps, filter),
			"-f", "null", "-",
		}
	}
	if metric == metricVMAF {
		out, err := tools.Output(ctx, build("libvmaf=n_subsample=2:n_threads="+threads))
		if m := vmafScore.FindStringSubmatch(out); err == nil && m != nil {
			s, _ := strconv.ParseFloat(m[1], 64)
			return s, metricVMAF, nil
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
	return s, metricSSIM, nil
}

// compareGraph arma el grafo de -lavfi que compara la salida ([0:v]) con el
// original ([1:v]) usando el filtro de la métrica (libvmaf o ssim).
func compareGraph(p Plan, w, h int, fps float64, metric string) string {
	return fmt.Sprintf("[0:v]%s[d];[1:v]%s[r];[d][r]%s", normalizeFilters(w, h, fps), referenceFilters(p, w, h, fps), metric)
}

// referenceFilters prepara el original para compararlo. Si era HDR pasa antes
// por el mismo tone mapping que la codificación, con el formato ya fijado por
// zimg (ver tonemapFilters): se compara SDR contra los mismos cuadros SDR que
// recibió el codificador, no contra otra conversión del mismo original.
func referenceFilters(p Plan, w, h int, fps float64) string {
	if p.Tonemap {
		return tonemapFilters + "," + normalizeFilters(w, h, fps)
	}
	return normalizeFilters(w, h, fps)
}

// normalizeFilters lleva un video al tamaño, los fps y el formato de píxel de
// la medición, con los tiempos desde cero para que los cuadros de los dos
// lados se correspondan uno a uno.
func normalizeFilters(w, h int, fps float64) string {
	return fmt.Sprintf("scale=%d:%d:flags=bicubic,fps=%s,setpts=PTS-STARTPTS,format=%s",
		w, h, strconv.FormatFloat(fps, 'f', -1, 64), sdrPixFmt)
}
