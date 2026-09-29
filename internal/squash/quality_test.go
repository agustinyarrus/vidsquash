package squash

// Tests del camino HDR: el tone mapping y la medición de calidad.
//
// La regla que cuidan: al medir, el original HDR se lleva a SDR con EXACTAMENTE
// la misma conversión que recibió el codificador. Si la codificación convierte
// con zimg y la referencia con swscale, la medición castiga una diferencia que
// la compresión no causó (el síntoma: un SSIM de 0,64 en un video que se veía
// bien). Los que corren ffmpeg generan sus propios originales HDR y se saltean
// si no hay un ffmpeg con zscale (libzimg).

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agustinyarrus/vidsquash/internal/ffx"
)

// La matriz de la conversión a YUV la aplica siempre zimg: en toda cadena con
// tone mapping, el zscale que pide m=bt709 va seguido de un format= que fija
// su salida. Sin eso, zscale negocia el formato con el filtro siguiente y, si
// es un scale, le entrega RGB flotante: la matriz la termina eligiendo swscale
// (BT.601 hasta ffmpeg 6.1), distinta según el camino y según la versión.
func TestTonemapFijaLaConversion(t *testing.T) {
	v := &ffx.VideoStream{Width: 3840, Height: 2160, FPS: 60, Transfer: "arib-std-b67"}
	hdr := func(w, h int, fps float64) Plan { return Plan{Width: w, Height: h, FPS: fps, Tonemap: true} }
	cadenas := []struct{ nombre, cadena string }{
		{"codificar sin cambios", videoFilters(hdr(3840, 2160, 60), v)},
		{"codificar achicando", videoFilters(hdr(1920, 1080, 60), v)},
		{"codificar a la mitad de fps", videoFilters(hdr(3840, 2160, 30), v)},
		{"codificar achicando y a 30 fps", videoFilters(hdr(1280, 720, 30), v)},
		{"referencia de la medición", referenceFilters(hdr(1280, 720, 30), 1920, 1080, 60)},
	}
	for _, c := range cadenas {
		filtros := strings.Split(c.cadena, ",")
		i := slices.IndexFunc(filtros, zscaleConMatriz)
		switch {
		case i < 0:
			t.Errorf("%s: falta el tone mapping en %q", c.nombre, c.cadena)
		case i+1 == len(filtros) || filtros[i+1] != "format="+sdrPixFmt:
			siguiente := "nada"
			if i+1 < len(filtros) {
				siguiente = filtros[i+1]
			}
			t.Errorf("%s: después de %q viene %s, no format=%s: la conversión a YUV queda en manos del filtro siguiente\n  %s",
				c.nombre, filtros[i], siguiente, sdrPixFmt, c.cadena)
		}
	}
}

// Un original SDR no pasa por el tone mapping en ningún lado, y en el grafo de
// la medición el tone mapping va del lado del original ([1:v]), nunca del de la
// salida ([0:v]), que ya es SDR.
func TestToneMappingSoloDelLadoDelOriginalHDR(t *testing.T) {
	sdr := Plan{Width: 1280, Height: 720, FPS: 30}
	v := &ffx.VideoStream{Width: 1920, Height: 1080, FPS: 30}
	for nombre, cadena := range map[string]string{
		"codificación SDR": videoFilters(sdr, v),
		"medición SDR":     compareGraph(sdr, 1920, 1080, 30, "ssim"),
	} {
		if strings.Contains(cadena, "zscale") || strings.Contains(cadena, "tonemap") {
			t.Errorf("%s: un original SDR no lleva tone mapping: %s", nombre, cadena)
		}
	}

	grafo := compareGraph(Plan{Width: 1280, Height: 720, FPS: 30, Tonemap: true}, 1920, 1080, 30, "ssim")
	salida, original := ramaDe(grafo, "[0:v]"), ramaDe(grafo, "[1:v]")
	if strings.Contains(salida, "tonemap") || !strings.Contains(original, tonemapFilters) {
		t.Errorf("el tone mapping tiene que ir solo del lado del original:\n  salida:   %s\n  original: %s", salida, original)
	}
}

// Sin cambio de tamaño ni de fps, la referencia de la medición es, bit a bit, lo
// que recibió el codificador. Es la prueba directa del bug: antes, el scale de
// la medición hacía que la conversión a YUV de la referencia la hiciera
// swscale, mientras que la del codificador la hacía zimg.
func TestReferenciaHDRIgualAlCodificador(t *testing.T) {
	tools := ffmpegHDR(t)
	for _, c := range []hdrClip{
		{nombre: "pq", transfer: "smpte2084", w: 640, h: 360},
		{nombre: "hlg", transfer: "arib-std-b67", w: 640, h: 360},
		{nombre: "hlg-vertical-rotado", transfer: "arib-std-b67", w: 640, h: 360, rotation: 90},
	} {
		t.Run(c.nombre, func(t *testing.T) {
			t.Parallel()
			orig := c.generar(t, tools)
			v := c.visible()
			p := Plan{Width: v.Width, Height: v.Height, FPS: v.FPS, Tonemap: true}
			grafo := fmt.Sprintf("[0:v]split[a][b];[a]%s[x];[b]%s[y];[x][y]psnr",
				videoFilters(p, v), referenceFilters(p, v.Width, v.Height, v.FPS))
			r := psnrDe(t, tools, orig, grafo)
			t.Log(r)
			if !math.IsInf(r.promedio, 1) {
				t.Errorf("la referencia difiere de lo que recibió el codificador (%s): se mediría una pérdida que la compresión no causó", r)
			}
		})
	}
}

// Al achicar un HDR, el codificador recibe BT.709, lo que dice la etiqueta del
// archivo, en cualquier versión de ffmpeg. Se compara contra el mismo original
// convertido sin achicar (la conversión que ya hacía zimg) y reducido después
// con zimg: la única diferencia legítima es el filtro de reducción.
func TestHDRAchicadoEnBT709(t *testing.T) {
	// Medido con ffmpeg 5.1 a 9.0: 54 dB en todas (solo difiere el filtro de
	// reducción). Sin el arreglo, 45 dB desde la 7.1 y 30 dB con la matriz de
	// BT.601 que usaba swscale hasta la 6.1.
	const minimo = 40.0
	tools := ffmpegHDR(t)
	c := hdrClip{nombre: "hlg", transfer: "arib-std-b67", w: 640, h: 360}
	orig := c.generar(t, tools)
	v := c.visible()
	w, h := scaledSize(v.Width, v.Height, 240)
	achicado := Plan{Width: w, Height: h, FPS: v.FPS, Tonemap: true}
	entero := Plan{Width: v.Width, Height: v.Height, FPS: v.FPS, Tonemap: true}
	grafo := fmt.Sprintf("[0:v]split[a][b];[a]%s[x];[b]%s,zscale=w=%d:h=%d:f=lanczos[y];[x][y]psnr",
		videoFilters(achicado, v), videoFilters(entero, v), w, h)
	r := psnrDe(t, tools, orig, grafo)
	t.Log(r)
	if r.promedio < minimo {
		t.Errorf("la versión achicada no está en BT.709 (%s; mínimo %.0f dB)", r, minimo)
	}
}

// De punta a punta, como lo corre vidsquash: ffprobe, el plan, las dos pasadas
// y la medición. Un HDR comprimido tiene que medir alto con SSIM (la métrica
// del síntoma) y con la que elige Measure, se achique o no, y vertical también.
func TestMedicionHDRDePuntaAPunta(t *testing.T) {
	if testing.Short() {
		t.Skip("codifica videos de verdad")
	}
	tools := ffmpegHDR(t)
	if motivo := faltaEncoder(tools, "libx264"); motivo != "" {
		t.Skip(motivo)
	}
	const (
		ssimMinimo = 0.95 // el síntoma del bug era 0,64
		vmafMinimo = 70.0
	)
	for _, c := range []struct {
		hdrClip
		objetivo int64
		achica   bool
	}{
		{hdrClip{nombre: "pq-sin-achicar", transfer: "smpte2084", w: 640, h: 360}, 600e3, false},
		{hdrClip{nombre: "hlg-achicado", transfer: "arib-std-b67", w: 640, h: 360}, 80e3, true},
		{hdrClip{nombre: "hlg-vertical-de-celular", transfer: "arib-std-b67", w: 640, h: 360, rotation: 90}, 600e3, false},
	} {
		t.Run(c.nombre, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), ffmpegTimeout)
			defer cancel()
			orig := c.generar(t, tools)
			m, err := tools.Probe(ctx, orig)
			if err != nil {
				t.Fatal(err)
			}
			if !m.Video.HDR() {
				t.Fatalf("ffprobe no lo ve como HDR (transferencia %q)", m.Video.Transfer)
			}
			p, err := MakePlan(m, Options{Target: c.objetivo})
			if err != nil {
				t.Fatal(err)
			}
			if achico := p.Width < m.Video.Width; !p.Tonemap || achico != c.achica {
				t.Fatalf("plan inesperado: %d×%d (original %d×%d), tone mapping %v", p.Width, p.Height, m.Video.Width, m.Video.Height, p.Tonemap)
			}

			salida := filepath.Join(t.TempDir(), "salida.mp4")
			res, err := Encode(ctx, tools, p, EncodeOptions{Input: orig, Output: salida, Preset: "veryfast", Video: m.Video}, nil)
			if err != nil {
				t.Fatal(err)
			}
			ssim, err := measure(ctx, tools, p, orig, 0, salida, m.Video, metricSSIM)
			if err != nil {
				t.Fatal(err)
			}
			q, err := Measure(ctx, tools, p, orig, 0, salida, m.Video)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%d×%d → %d×%d · %s · SSIM %.4f · %s %.4g", m.Video.Width, m.Video.Height, p.Width, p.Height, humanBytes(res.Size), ssim.Score, q.Metric, q.Score)
			if ssim.Score < ssimMinimo {
				t.Errorf("SSIM %.4f: por debajo de %.2f, la medición castiga algo que no es la compresión", ssim.Score, ssimMinimo)
			}
			if q.Metric == metricVMAF && q.Score < vmafMinimo {
				t.Errorf("VMAF %.1f: por debajo de %.0f", q.Score, vmafMinimo)
			}
		})
	}
}

// --- originales HDR sintéticos ------------------------------------------------

// ffmpegTimeout es el techo de cada llamada a ffmpeg de estos tests: si algo se
// cuelga, el proceso muere con el contexto en vez de quedar huérfano.
const ffmpegTimeout = 2 * time.Minute

const (
	clipFPS  = 30
	clipSecs = 2
)

// hdrClip describe un original HDR sintético: testsrc2 (colores saturados,
// bordes finos y movimiento) llevado a BT.2020 con la curva pedida, en ProRes
// de 10 bits dentro de un .mov, como lo graba un celular en modo HDR.
type hdrClip struct {
	nombre   string
	transfer string // smpte2084 (PQ) o arib-std-b67 (HLG)
	w, h     int    // tamaño codificado
	rotation int    // matriz de visualización, en grados (0 = sin rotar)
}

// visible es la pista como la ve ffprobe después de aplicar la rotación.
func (c hdrClip) visible() *ffx.VideoStream {
	v := &ffx.VideoStream{Width: c.w, Height: c.h, FPS: clipFPS, Transfer: c.transfer, Rotation: c.rotation}
	if c.rotation == 90 || c.rotation == 270 {
		v.Width, v.Height = v.Height, v.Width
	}
	return v
}

func (c hdrClip) generar(t *testing.T, tools ffx.Tools) string {
	t.Helper()
	dir := t.TempDir()
	ruta := filepath.Join(dir, c.nombre+".mov")
	correr(t, tools,
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=s=%dx%d:r=%d:d=%d", c.w, c.h, clipFPS, clipSecs),
		"-vf", "zscale=tin=bt709:pin=bt709:min=bt709:rin=tv:t="+c.transfer+":p=bt2020:m=bt2020nc:r=tv:npl=100,format=yuv422p10le",
		"-c:v", "prores_ks", "-profile:v", "3",
		"-color_primaries", "bt2020", "-color_trc", c.transfer, "-colorspace", "bt2020nc", "-color_range", "tv",
		"-y", ruta)
	if c.rotation == 0 {
		return ruta
	}
	rotado := filepath.Join(dir, c.nombre+"-rotado.mov")
	ctx, cancel := context.WithTimeout(t.Context(), ffmpegTimeout)
	defer cancel()
	if _, err := tools.Output(ctx, []string{"-display_rotation:v:0", strconv.Itoa(c.rotation), "-i", ruta, "-c", "copy", "-y", rotado}); err != nil {
		t.Skipf("este ffmpeg no sabe escribir la rotación (la 5.1 no tiene -display_rotation; la 6.1 sí): %v", err)
	}
	return rotado
}

// --- ffmpeg -------------------------------------------------------------------

// ffmpegHDR devuelve un ffmpeg capaz de procesar HDR (zscale de libzimg y
// tonemap) o saltea el test. La búsqueda se hace una sola vez para todos.
func ffmpegHDR(t *testing.T) ffx.Tools {
	t.Helper()
	tools, motivo := buscarFFmpegHDR()
	if motivo != "" {
		t.Skip(motivo)
	}
	return tools
}

var buscarFFmpegHDR = sync.OnceValues(func() (ffx.Tools, string) {
	tools, err := ffx.Locate()
	if err != nil {
		return tools, err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), ffmpegTimeout)
	defer cancel()
	out, err := tools.Output(ctx, []string{"-filters"})
	if err != nil {
		return tools, "ffmpeg -filters falló: " + err.Error()
	}
	disponibles := columna(out, 1)
	for _, f := range []string{"zscale", "tonemap", "psnr", "ssim"} {
		if !disponibles[f] {
			return tools, "este ffmpeg no trae el filtro " + f + " (hace falta una compilación con libzimg)"
		}
	}
	return tools, ""
})

// faltaEncoder dice por qué no se puede codificar con name, o "" si se puede.
func faltaEncoder(tools ffx.Tools, name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), ffmpegTimeout)
	defer cancel()
	out, err := tools.Output(ctx, []string{"-encoders"})
	if err != nil {
		return "ffmpeg -encoders falló: " + err.Error()
	}
	if !columna(out, 1)[name] {
		return "este ffmpeg no trae " + name
	}
	return ""
}

// columna junta la palabra n de cada renglón: los listados de ffmpeg traen las
// banderas en la primera columna y el nombre en la segunda.
func columna(listado string, n int) map[string]bool {
	set := make(map[string]bool)
	for _, renglon := range strings.Split(listado, "\n") {
		if f := strings.Fields(renglon); len(f) > n {
			set[f[n]] = true
		}
	}
	return set
}

func correr(t *testing.T, tools ffx.Tools, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), ffmpegTimeout)
	defer cancel()
	out, err := tools.Output(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var psnrLinea = regexp.MustCompile(`PSNR y:(\S+) u:(\S+) v:(\S+) average:(\S+)`)

// resultadoPSNR es lo que informa el filtro psnr al final, en dB por plano.
type resultadoPSNR struct {
	y, u, v, promedio float64
}

func (r resultadoPSNR) String() string {
	return fmt.Sprintf("PSNR %s dB · Y %s · U %s · V %s", dB(r.promedio), dB(r.y), dB(r.u), dB(r.v))
}

func dB(x float64) string {
	if math.IsInf(x, 1) {
		return "∞"
	}
	return strconv.FormatFloat(x, 'f', 1, 64)
}

// psnrDe corre sobre orig un grafo que termina en psnr. ParseFloat entiende el
// "inf" de ffmpeg: cuadros idénticos bit a bit dan +Inf.
func psnrDe(t *testing.T, tools ffx.Tools, orig, grafo string) resultadoPSNR {
	t.Helper()
	out := correr(t, tools, "-i", orig, "-lavfi", grafo, "-f", "null", "-")
	m := psnrLinea.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("ffmpeg no informó el PSNR:\n%s", out)
	}
	var vals [4]float64
	for i := range vals {
		f, err := strconv.ParseFloat(m[i+1], 64)
		if err != nil {
			t.Fatalf("PSNR ilegible %q: %v", m[i+1], err)
		}
		vals[i] = f
	}
	return resultadoPSNR{y: vals[0], u: vals[1], v: vals[2], promedio: vals[3]}
}

// --- cadenas de filtros ---------------------------------------------------------

// zscaleConMatriz dice si el filtro es un zscale que fija la matriz de YUV.
func zscaleConMatriz(filtro string) bool {
	opts, ok := strings.CutPrefix(filtro, "zscale=")
	if !ok {
		return false
	}
	for _, o := range strings.Split(opts, ":") {
		if k, _, _ := strings.Cut(o, "="); k == "m" || k == "matrix" {
			return true
		}
	}
	return false
}

// ramaDe devuelve la cadena del grafo que empieza con la etiqueta de entrada.
func ramaDe(grafo, entrada string) string {
	for _, rama := range strings.Split(grafo, ";") {
		if cadena, ok := strings.CutPrefix(rama, entrada); ok {
			return cadena
		}
	}
	return ""
}
