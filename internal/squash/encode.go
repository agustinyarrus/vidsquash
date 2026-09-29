package squash

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/agustinyarrus/vidsquash/internal/ffx"
)

// encode.go ejecuta la receta: primera pasada (el codificador estudia el video
// entero y anota dónde hacen falta bits), segunda pasada (reparte el
// presupuesto con esa información) y, si el archivo se pasó del límite o quedó
// muy por debajo, más segundas pasadas corregidas. Las estadísticas de la
// primera pasada valen para cualquier bitrate, así que corregir no obliga a
// repetirla.
//
// La corrección es el método de la secante sobre tamaño(bitrate), que es
// monótona: con un solo intento se usa la proporción directa; con dos, la
// secante entre los dos últimos puntos. Converge en uno o dos pasos.

const (
	maxCorrections   = 3
	acceptLowFrac    = 0.90  // debajo del 90 % del objetivo se desperdicia calidad: se corrige
	aimFrac          = 0.985 // a dónde apunta una corrección (margen para el error del codificador)
	lastTrySafety    = 0.97  // la última corrección apunta más abajo: tiene que entrar sí o sí
	videoStreamIndex = "0:v:0"
)

// EncodeOptions son los parámetros de ejecución.
type EncodeOptions struct {
	Input  string
	Output string // ruta final: se escribe a un temporal al lado y se renombra
	From   time.Duration
	Preset string // de x264/x265: veryfast … slower
	Video  *ffx.VideoStream
}

// Phase informa en qué etapa va la codificación, para la barra.
type Phase struct {
	Name string
	Step int
	Frac float64
	Prog ffx.Progress
}

// Attempt es una segunda pasada: con qué bitrate y qué tamaño dio.
type Attempt struct {
	VideoKbps int
	Size      int64
}

// Result resume la codificación.
type Result struct {
	Size      int64
	VideoKbps int
	Attempts  []Attempt
}

// Encode corre las pasadas y deja el mejor intento (el más grande que entra)
// en opt.Output.
func Encode(ctx context.Context, tools ffx.Tools, p Plan, opt EncodeOptions, onPhase func(Phase)) (Result, error) {
	work, err := os.MkdirTemp("", "vidsquash-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(work) // estadísticas de pasadas y archivos de intentos
	passlog := filepath.Join(work, "pasada")
	step := 0
	report := func(name string) func(ffx.Progress) {
		step++
		s := step
		return func(pr ffx.Progress) {
			if onPhase != nil {
				onPhase(Phase{Name: name, Step: s, Frac: frac(pr.OutTime, p.Duration), Prog: pr})
			}
		}
	}

	if err := tools.Run(ctx, pass1Args(p, opt, passlog), report("Primera pasada: estudiando el video")); err != nil {
		return Result{}, err
	}

	fixed := float64(p.Overhead) + float64(p.AudioKbps)*1000/8*p.Duration.Seconds()
	var res Result
	best := ""
	vk := p.VideoKbps
	for attempt := 0; attempt <= maxCorrections; attempt++ {
		name := "Segunda pasada: codificando"
		if attempt > 0 {
			name = fmt.Sprintf("Ajuste fino %d: %s de video", attempt, kbps(vk))
		}
		out := filepath.Join(work, fmt.Sprintf("intento-%d.mp4", attempt))
		if err := tools.Run(ctx, pass2Args(p, opt, passlog, vk, out), report(name)); err != nil {
			return Result{}, err
		}
		st, err := os.Stat(out)
		if err != nil {
			return Result{}, err
		}
		res.Attempts = append(res.Attempts, Attempt{VideoKbps: vk, Size: st.Size()})
		if st.Size() <= p.Target && (best == "" || st.Size() > res.Size) {
			best, res.Size, res.VideoKbps = out, st.Size(), vk
		}
		fits := st.Size() <= p.Target
		if fits && float64(st.Size()) >= acceptLowFrac*float64(p.Target) {
			break // entra y aprovecha el presupuesto: listo
		}
		if attempt == maxCorrections {
			break
		}
		aim := aimFrac
		if attempt == maxCorrections-1 {
			aim = lastTrySafety
		}
		next := nextBitrate(res.Attempts, float64(p.Target)*aim, fixed)
		if next == vk {
			break // no hay nada que ajustar
		}
		vk = next
	}
	if best == "" {
		return res, fmt.Errorf("después de %d intentos el video sigue pasándose del objetivo (%s)", len(res.Attempts), humanBytes(res.Attempts[len(res.Attempts)-1].Size))
	}
	if err := replace(best, opt.Output); err != nil {
		return res, err
	}
	return res, nil
}

// nextBitrate predice el bitrate de video que lleva el tamaño total a want.
// Con un intento: proporción directa sobre la parte de video (el audio y el
// contenedor son fijos). Con dos o más: secante entre los dos últimos.
func nextBitrate(attempts []Attempt, want, fixed float64) int {
	last := attempts[len(attempts)-1]
	videoWant := want - fixed
	videoGot := float64(last.Size) - fixed
	var next float64
	if len(attempts) >= 2 {
		prev := attempts[len(attempts)-2]
		ds := float64(last.Size - prev.Size)
		if ds != 0 {
			next = float64(last.VideoKbps) + (want-float64(last.Size))*float64(last.VideoKbps-prev.VideoKbps)/ds
		}
	}
	if next <= 0 && videoGot > 0 {
		next = float64(last.VideoKbps) * videoWant / videoGot
	}
	if next <= 0 || math.IsNaN(next) || math.IsInf(next, 0) {
		next = float64(last.VideoKbps) * 0.9
	}
	return max(minVideoKbps, int(math.Floor(next)))
}

// videoFilters arma la cadena: tone mapping (si es HDR), escala, fps y el
// formato de píxel que todo reproductor entiende.
func videoFilters(p Plan, v *ffx.VideoStream) string {
	var f []string
	if p.Tonemap {
		f = append(f, tonemapFilters)
	}
	if v == nil || p.Width != v.Width || p.Height != v.Height {
		f = append(f, fmt.Sprintf("scale=%d:%d:flags=lanczos", p.Width, p.Height))
	}
	if v == nil || math.Abs(p.FPS-v.FPS) > 0.01 {
		f = append(f, "fps="+strconv.FormatFloat(p.FPS, 'f', -1, 64))
	}
	return strings.Join(append(f, "format="+sdrPixFmt), ",")
}

// sdrPixFmt es el formato de píxel de la salida: 4:2:0 de 8 bits, el que todo
// reproductor entiende. En ese mismo formato se comparan los cuadros al medir.
const sdrPixFmt = "yuv420p"

// tonemapFilters lleva HDR (PQ o HLG) a SDR BT.709 con la curva Hable, pasando
// por luz lineal en punto flotante (zscale, de la biblioteca zimg).
//
// El format= del final fija la salida del último zscale. Sin él, zscale negocia
// el formato con el filtro que sigue: si es un scale (al achicar el video, y
// siempre en la medición), gana su RGB flotante de trabajo, el m=bt709:r=tv no
// se aplica y la conversión a YUV la hace swscale, con BT.601 hasta ffmpeg 6.1
// en un archivo marcado BT.709. Fijado, la hace siempre zimg: la codificación y
// la referencia de la medición reciben exactamente los mismos cuadros SDR.
const tonemapFilters = "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709," +
	"tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=" + sdrPixFmt

func inputArgs(p Plan, opt EncodeOptions) []string {
	var a []string
	if opt.From > 0 {
		a = append(a, "-ss", secs(opt.From)) // antes de -i: búsqueda rápida y exacta al transcodificar
	}
	a = append(a, "-i", opt.Input, "-t", secs(p.Duration))
	return a
}

func codecArgs(p Plan, opt EncodeOptions, vk int, pass int, passlog string) []string {
	preset := opt.Preset
	if preset == "" {
		preset = "medium"
	}
	switch p.Codec {
	case H265:
		return []string{"-c:v", "libx265", "-preset", preset, "-b:v", fmt.Sprintf("%dk", vk),
			"-x265-params", fmt.Sprintf("pass=%d:stats=%s:log-level=error", pass, filepath.ToSlash(passlog+".x265")),
			"-tag:v", "hvc1"} // hvc1: sin esta etiqueta, iPhone y Mac no lo reproducen
	default:
		return []string{"-c:v", "libx264", "-preset", preset, "-b:v", fmt.Sprintf("%dk", vk),
			"-pass", strconv.Itoa(pass), "-passlogfile", passlog}
	}
}

func pass1Args(p Plan, opt EncodeOptions, passlog string) []string {
	a := append([]string{"-y"}, inputArgs(p, opt)...)
	a = append(a, "-map", videoStreamIndex, "-vf", videoFilters(p, opt.Video))
	a = append(a, codecArgs(p, opt, p.VideoKbps, 1, passlog)...)
	return append(a, "-an", "-f", "null", os.DevNull)
}

func pass2Args(p Plan, opt EncodeOptions, passlog string, vk int, out string) []string {
	a := append([]string{"-y"}, inputArgs(p, opt)...)
	a = append(a, "-map", videoStreamIndex)
	if p.AudioKbps > 0 {
		a = append(a, "-map", "0:a:0?")
	}
	a = append(a, "-vf", videoFilters(p, opt.Video))
	a = append(a, codecArgs(p, opt, vk, 2, passlog)...)
	if p.AudioKbps > 0 {
		a = append(a, "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", p.AudioKbps), "-ac", strconv.Itoa(p.AudioChannels))
	} else {
		a = append(a, "-an")
	}
	// faststart: el índice va al principio y el video arranca antes de bajarse
	// entero (lo que hace falta para verlo en el navegador o en un chat).
	return append(a, "-map_metadata", "0", "-movflags", "+faststart", "-f", "mp4", out)
}

func secs(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}

func frac(done, total time.Duration) float64 {
	if total <= 0 {
		return 0
	}
	return max(0, min(1, float64(done)/float64(total)))
}

func kbps(k int) string {
	if k >= 1000 {
		return strconv.FormatFloat(float64(k)/1000, 'f', 2, 64) + " Mb/s"
	}
	return strconv.Itoa(k) + " kb/s"
}

// replace mueve el intento elegido a la ruta final (mismo volumen: rename; si
// no, copia). Reintenta por si un antivirus tiene el archivo un instante.
func replace(from, to string) error {
	var err error
	for i, delay := 0, 40*time.Millisecond; i < 5; i, delay = i+1, delay*2 {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(delay)
	}
	data, rerr := os.ReadFile(from)
	if rerr != nil {
		return err
	}
	tmp := to + ".tmp"
	if werr := os.WriteFile(tmp, data, 0o644); werr != nil {
		return werr
	}
	return os.Rename(tmp, to)
}
