// Package squash planifica y ejecuta la compresión de un video a un tamaño
// objetivo: cuántos bits hay, cuántos van al audio, qué resolución y cuadros
// por segundo aprovechan mejor los que quedan, y una codificación de dos
// pasadas que corrige hasta caer debajo del límite.
package squash

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/agustinyarrus/vidsquash/internal/ffx"
)

// Codec es el códec de video de salida.
type Codec string

const (
	H264 Codec = "h264" // se reproduce en todos lados (WhatsApp, Discord, iPhone, navegadores)
	H265 Codec = "h265" // ~30 % más eficiente; no todos los reproductores lo abren
)

// bppGood es el mínimo de bits por píxel (por cuadro) con el que cada códec
// todavía da una imagen limpia en contenido real. Por debajo, se prefiere bajar
// la resolución o los cuadros por segundo antes que estrujar la imagen.
var bppGood = map[Codec]float64{H264: 0.050, H265: 0.035}

// shortSides es la escalera de resoluciones, por el LADO CORTO: un video
// vertical de celular (1080×1920) es "1080p" igual que uno horizontal.
var shortSides = []int{2160, 1440, 1080, 720, 540, 480, 360, 240}

const (
	minVideoKbps     = 45   // menos que esto ya no es video, es un mosaico
	mp4FixedOverhead = 4096 // ftyp + moov mínimo
	mp4BytesPerVideo = 12   // stsz + ctts + stts/stss por cuadro de video, promedio
	mp4BytesPerAudio = 6    // stsz + stco por cuadro AAC (1024 muestras)
	aacFrameSamples  = 1024
)

// Options son las decisiones que puede forzar el user.
type Options struct {
	Target   int64         // bytes del archivo final
	Codec    Codec         //
	NoAudio  bool          // descartar el audio
	KeepFPS  bool          // no bajar los cuadros por segundo
	MaxShort int           // tope del lado corto (0 = el del original)
	From, To time.Duration // recorte (To = 0: hasta el final)
	Margin   float64       // fracción de seguridad bajo el objetivo (0,02 = 2 %)
}

// Tier es una resolución evaluada, con los bits por píxel que le tocarían.
type Tier struct {
	Width, Height int
	FPS           float64
	BPP           float64
}

// Plan es la receta de la compresión.
type Plan struct {
	Duration      time.Duration
	Width, Height int
	FPS           float64
	VideoKbps     int
	AudioKbps     int // 0 = sin audio
	AudioChannels int
	Overhead      int64
	BPP           float64
	Tonemap       bool
	Reasons       []string // por qué se eligió cada cosa, para mostrar
	Considered    []Tier
	SourceFPS     float64
	Target        int64
	Codec         Codec
}

// ErrTooSmall se devuelve cuando el objetivo no alcanza ni para un video mínimo.
var ErrTooSmall = errors.New("el tamaño pedido no alcanza para ese video")

// MakePlan arma la receta. Es una función pura (sin E/S): recibe lo que dijo
// ffprobe y las opciones, y devuelve la receta o por qué no se puede.
func MakePlan(m *ffx.Media, opt Options) (Plan, error) {
	if m.Video == nil {
		return Plan{}, errors.New("el archivo no tiene pista de video")
	}
	if opt.Codec == "" {
		opt.Codec = H264
	}
	if opt.Margin <= 0 {
		opt.Margin = 0.02
	}
	dur := effectiveDuration(m.Duration, opt.From, opt.To)
	if dur <= 0 {
		return Plan{}, errors.New("el recorte deja el video vacío")
	}
	secs := dur.Seconds()
	p := Plan{Duration: dur, SourceFPS: m.Video.FPS, Target: opt.Target, Codec: opt.Codec, Tonemap: m.Video.HDR()}

	budget := float64(opt.Target) * (1 - opt.Margin)
	overhead := estimateOverhead(secs, m.Video.FPS, m.Audio != nil && !opt.NoAudio)
	totalKbps := (budget - float64(overhead)) * 8 / secs / 1000
	if totalKbps <= minVideoKbps {
		return Plan{}, tooSmall(m, opt, dur)
	}

	p.AudioKbps, p.AudioChannels = chooseAudio(totalKbps, m.Audio, opt.NoAudio)
	videoKbps := totalKbps - float64(p.AudioKbps)
	if videoKbps < minVideoKbps {
		return Plan{}, tooSmall(m, opt, dur)
	}

	tier, considered, why := chooseTier(m.Video, videoKbps*1000, opt)
	p.Considered = considered
	p.Width, p.Height, p.FPS = tier.Width, tier.Height, tier.FPS

	// Con los fps elegidos, el overhead real es un poco menor: se recalcula.
	p.Overhead = estimateOverhead(secs, p.FPS, p.AudioKbps > 0)
	totalKbps = (budget - float64(p.Overhead)) * 8 / secs / 1000
	p.VideoKbps = int(math.Floor(totalKbps)) - p.AudioKbps
	p.BPP = float64(p.VideoKbps*1000) / (float64(p.Width*p.Height) * p.FPS)
	p.Reasons = append(p.Reasons, why...)
	if p.Tonemap {
		p.Reasons = append(p.Reasons, "HDR → SDR (tone mapping), para que no se vea lavado en pantallas comunes")
	}
	return p, nil
}

func effectiveDuration(total, from, to time.Duration) time.Duration {
	end := total
	if to > 0 && to < end {
		end = to
	}
	if from < 0 {
		from = 0
	}
	return end - from
}

// estimateOverhead modela lo que el contenedor MP4 suma a los datos: fijo más
// una entrada por cuadro de video y de audio en las tablas del moov.
func estimateOverhead(secs, fps float64, audio bool) int64 {
	o := float64(mp4FixedOverhead) + secs*fps*mp4BytesPerVideo
	if audio {
		o += secs * 48000 / aacFrameSamples * mp4BytesPerAudio
	}
	return int64(math.Ceil(o))
}

// chooseAudio reparte: con pocos bits, el audio cede primero calidad y después
// canales (mono suena mucho mejor que un estéreo estrujado); nunca se infla por
// encima del original.
func chooseAudio(totalKbps float64, a *ffx.AudioStream, noAudio bool) (kbps, channels int) {
	if a == nil || noAudio {
		return 0, 0
	}
	switch {
	case totalKbps >= 2500:
		kbps = 160
	case totalKbps >= 1200:
		kbps = 128
	case totalKbps >= 600:
		kbps = 96
	case totalKbps >= 300:
		kbps = 64
	case totalKbps >= 150:
		kbps = 48
	default:
		kbps = 32
	}
	if a.BitRate > 0 {
		if src := int(a.BitRate / 1000); src >= 32 && src < kbps {
			kbps = src
		}
	}
	channels = a.Channels
	if channels <= 0 || channels > 2 {
		channels = 2 // 5.1 → estéreo: ningún destino de "mandalo por chat" usa 5.1
	}
	if kbps < 64 {
		channels = 1
	}
	return kbps, channels
}

// chooseTier recorre la escalera de mayor a menor y se queda con la primera
// combinación (resolución, fps) cuyos bits por píxel alcanzan. A igual
// resolución prueba primero los fps originales y después la mitad (60 → 30):
// en contenido común se ve mejor más resolución a 30 que menos a 60.
func chooseTier(v *ffx.VideoStream, videoBps float64, opt Options) (Tier, []Tier, []string) {
	short := min(v.Width, v.Height)
	fpsOptions := []float64{v.FPS}
	if !opt.KeepFPS && v.FPS > 30.5 && v.FPS/2 >= 23.9 {
		fpsOptions = append(fpsOptions, v.FPS/2)
	}
	threshold := bppGood[opt.Codec]

	var tiers []Tier
	for _, s := range ladderFor(short, opt.MaxShort) {
		w, h := scaledSize(v.Width, v.Height, s)
		for _, f := range fpsOptions {
			tiers = append(tiers, Tier{Width: w, Height: h, FPS: f, BPP: videoBps / (float64(w*h) * f)})
		}
	}
	for _, t := range tiers {
		need := threshold
		if t.FPS < v.FPS {
			// Sesgo deliberado y documentado: a la variante de fps reducidos de
			// una resolución se le perdona un 10 %, para que un empate técnico
			// no baje la resolución (la regla es "resolución antes que fps").
			need *= 0.9
		}
		if t.BPP >= need {
			return t, tiers, explain(v, t)
		}
	}
	last := tiers[len(tiers)-1]
	why := explain(v, last)
	why = append(why, fmt.Sprintf("aun así quedan pocos bits (%.3f bits/píxel): la imagen va a verse gruesa; conviene recortar el video o pedir más tamaño", last.BPP))
	return last, tiers, why
}

// ladderFor devuelve los lados cortos a probar: el original (si no está en la
// escalera) y todos los menores; nunca se agranda.
func ladderFor(short, maxShort int) []int {
	top := short
	if maxShort > 0 && maxShort < top {
		top = maxShort
	}
	out := []int{top}
	for _, s := range shortSides {
		if s < top {
			out = append(out, s)
		}
	}
	return out
}

// scaledSize escala manteniendo la proporción hasta que el lado corto valga
// short, redondeando a par (lo exige yuv420p).
func scaledSize(w, h, short int) (int, int) {
	if min(w, h) == short {
		return even(w), even(h)
	}
	scale := float64(short) / float64(min(w, h))
	return even(int(math.Round(float64(w) * scale))), even(int(math.Round(float64(h) * scale)))
}

func even(n int) int {
	if n%2 == 1 {
		n--
	}
	return max(n, 2)
}

func explain(v *ffx.VideoStream, t Tier) []string {
	var why []string
	if t.Width != v.Width || t.Height != v.Height {
		why = append(why, fmt.Sprintf("resolución %d×%d → %d×%d para que cada píxel tenga bits suficientes", v.Width, v.Height, t.Width, t.Height))
	}
	if math.Abs(t.FPS-v.FPS) > 0.01 {
		why = append(why, fmt.Sprintf("%s → %s cuadros por segundo: la mitad de cuadros, el doble de bits por cuadro", fmtFPS(v.FPS), fmtFPS(t.FPS)))
	}
	return why
}

func fmtFPS(f float64) string {
	if math.Abs(f-math.Round(f)) < 0.01 {
		return fmt.Sprintf("%.0f", f)
	}
	return fmt.Sprintf("%.2f", f)
}

// tooSmall arma un error útil: cuánto haría falta como mínimo, o cuánto habría
// que recortar para entrar con el tamaño pedido.
func tooSmall(m *ffx.Media, opt Options, dur time.Duration) error {
	secs := dur.Seconds()
	minBytes := int64(float64(minVideoKbps+32)*1000/8*secs) + estimateOverhead(secs, 15, m.Audio != nil)
	maxSecs := float64(opt.Target) * 8 / float64((minVideoKbps+32)*1000)
	return fmt.Errorf("%w: para %s harían falta al menos %s, o recortarlo a unos %s con --to",
		ErrTooSmall, clock(dur), humanBytes(minBytes), clock(time.Duration(maxSecs*float64(time.Second))))
}

func clock(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
	return fmt.Sprintf("%.0f KB", float64(n)/1e3)
}
