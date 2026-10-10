// Package vidsquash implementa `vidsquash`: comprime un video para que entre en
// un tamaño dado (el límite de Discord, WhatsApp, un mail) con la mejor calidad
// posible, eligiendo solo la resolución, los fps y el reparto de bits.
package vidsquash

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/agustinyarrus/vidsquash/internal/cli"
	"github.com/agustinyarrus/vidsquash/internal/ffx"
	"github.com/agustinyarrus/vidsquash/internal/fsx"
	"github.com/agustinyarrus/vidsquash/internal/i18n"
	"github.com/agustinyarrus/vidsquash/internal/squash"
	"github.com/agustinyarrus/vidsquash/internal/tui"
)

// presets son los límites publicados de cada servicio (bytes decimales). Si
// cambian, -s manda.
var presets = map[string]int64{
	"discord":  10e6,
	"whatsapp": 16e6,
	"outlook":  20e6,
	"gmail":    25e6,
}

type opciones struct {
	tamano   string
	para     string
	salida   string
	codec    string
	preset   string
	desde    string
	hasta    string
	sinAudio bool
	mantener bool
	maxRes   int
	sinMedir bool
	forzar   bool
}

// Main es el punto de entrada de vidsquash: interpreta args, hace el trabajo y
// devuelve el código de salida.
func Main(t *tui.Term, version string, args []string) int {
	cli.ResolveLang("vidsquash", args) // el idioma, antes de escribir una sola palabra
	o := opciones{codec: "h264", preset: "medium"}
	app := cli.New("vidsquash", version, i18n.T("comprime un video para que entre en un tamaño"))
	app.Usage = []string{
		i18n.T("vidsquash <video> -s <tamaño> [opciones]"),
		i18n.T("vidsquash clip.mp4 -s 25MB"),
		i18n.T("vidsquash clip.mp4 --for discord"),
	}
	app.Section(i18n.T("objetivo"))
	app.String(&o.tamano, "size", 's', i18n.T("TAMAÑO"), i18n.T("tamaño máximo del archivo final: 25MB, 8M, 1,5GB, 500KiB"))
	app.Enum(&o.para, "for", 0, i18n.T("límite de un servicio: discord 10 MB · whatsapp 16 MB · outlook 20 MB · gmail 25 MB"), "discord", "whatsapp", "outlook", "gmail")
	app.Section(i18n.T("recorte"))
	app.String(&o.desde, "from", 0, i18n.T("TIEMPO"), i18n.T("empezar en este punto (90, 1:30, 1m30s)"))
	app.String(&o.hasta, "to", 0, i18n.T("TIEMPO"), i18n.T("terminar en este punto"))
	app.Section(i18n.T("receta"))
	app.Enum(&o.codec, "codec", 'c', i18n.T("h264 se reproduce en todos lados; h265 ocupa ~30 % menos pero no todos lo abren"), "h264", "h265")
	app.Enum(&o.preset, "preset", 'p', i18n.T("esfuerzo del codificador: más lento = mejor calidad al mismo tamaño"), "veryfast", "faster", "fast", "medium", "slow", "slower")
	app.Int(&o.maxRes, "max-res", 0, "N", i18n.T("tope del lado corto (720 = como mucho 720p)"), 0, 4320)
	app.Bool(&o.mantener, "keep-fps", 0, i18n.T("no bajar los cuadros por segundo (juegos, deportes)"))
	app.Bool(&o.sinAudio, "no-audio", 0, i18n.T("descartar el audio (todos los bits al video)"))
	app.Section(i18n.T("salida"))
	app.String(&o.salida, "out", 'o', i18n.T("ARCHIVO"), i18n.T("archivo de salida (por defecto: <nombre>-<tamaño>.mp4 al lado del original)"))
	app.Bool(&o.forzar, "force", 'f', i18n.T("sobrescribir la salida si ya existe"))
	app.Bool(&o.sinMedir, "no-quality", 0, i18n.T("no medir la calidad al final (ahorra unos segundos)"))
	app.Examples = []cli.Example{
		{Cmd: i18n.T("vidsquash partida.mp4 --for discord"), Desc: i18n.T("que entre en los 10 MB de Discord")},
		{Cmd: i18n.T("vidsquash viaje.mov -s 16MB"), Desc: i18n.T("para WhatsApp; un video HDR de iPhone se convierte a SDR")},
		{Cmd: i18n.T("vidsquash charla.mp4 -s 25MB --from 2:10 --to 5:40"), Desc: i18n.T("solo un tramo, para mandarlo por mail")},
		{Cmd: i18n.T("vidsquash juego.mp4 -s 50MB --keep-fps -p slow"), Desc: i18n.T("conservar los 60 fps y exprimir calidad")},
	}
	app.Notes = []string{
		i18n.T("Los tamaños van en unidades decimales (1 MB = 1.000.000 bytes), como los anuncian los servicios."),
		i18n.T("Codifica en dos pasadas y, si el resultado se pasa del límite, corrige solo la segunda: nunca entrega un archivo más grande que el pedido."),
		i18n.T("Con pocos bits, primero baja los cuadros por segundo (60 → 30) y después la resolución: en contenido común se ve mejor."),
		i18n.T("Necesita ffmpeg (winget install Gyan.FFmpeg). Todo es local: el video no sale de la máquina."),
	}

	pos, done, code := app.Start(t, args)
	if done {
		return code
	}
	if len(pos) != 1 {
		t.Lines(app.Help(t))
		return cli.ExitUsage
	}
	cfg, err := validar(o, pos[0])
	if err != nil {
		return fallo(t, err)
	}

	tools, err := ffx.Locate()
	if err != nil {
		return fallo(t, err)
	}
	ctx, stop := cli.Interrupt(nil)
	defer stop()

	t.Lines(t.Header("vidsquash", i18n.T("comprime un video para que entre en un tamaño"), version))
	media, err := tools.Probe(ctx, cfg.entrada)
	if err != nil {
		return fallo(t, err)
	}
	plan, err := squash.MakePlan(media, cfg.plan)
	if err != nil {
		return fallo(t, err)
	}
	t.Lines(bloqueReceta(t, media, plan, cfg))

	inicio := time.Now()
	res, err := codificar(ctx, t, tools, plan, cfg, media)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			t.Line(t.Status(tui.Skip, i18n.T("Cancelado"), i18n.T("no quedó ningún archivo a medias")))
			return cli.ExitInterrupted
		}
		return fallo(t, err)
	}

	var q *squash.Quality
	if !o.sinMedir {
		if qq, err := medir(ctx, t, tools, plan, cfg, media); err == nil {
			q = &qq
		} else if !errors.Is(err, context.Canceled) {
			t.Line(t.Status(tui.Warn, i18n.T("No pude medir la calidad"), err.Error()))
		}
	}
	t.Lines(tarjeta(t, media, plan, res, q, cfg, time.Since(inicio)))
	return cli.ExitOK
}

// config es lo validado en la frontera.
type config struct {
	entrada  string
	salida   string
	plan     squash.Options
	preset   string
	etiqueta string // "25 MB" o "Discord (10 MB)"
}

func validar(o opciones, entrada string) (config, error) {
	st, err := os.Stat(entrada)
	if errors.Is(err, fs.ErrNotExist) {
		return config{}, i18n.Errorf("%s no existe", fsx.Display(entrada))
	}
	if err != nil {
		return config{}, fmt.Errorf("%s: %w", entrada, err)
	}
	if st.IsDir() {
		return config{}, i18n.Errorf("%s es una carpeta; vidsquash trabaja con un video por vez", entrada)
	}
	var target int64
	etiqueta := ""
	switch {
	case o.tamano != "":
		target, err = cli.ParseSize(o.tamano)
		if err != nil {
			return config{}, err
		}
		etiqueta = tui.Bytes(target)
	case o.para != "":
		target = presets[o.para]
		etiqueta = fmt.Sprintf("%s (%s)", strings.ToUpper(o.para[:1])+o.para[1:], tui.Bytes(target))
	default:
		return config{}, i18n.Errorf("falta el tamaño objetivo: -s 25MB o --for discord")
	}
	if target < 100e3 {
		return config{}, i18n.Errorf("%s es demasiado poco para un video", tui.Bytes(target))
	}
	var from, to time.Duration
	if o.desde != "" {
		if from, err = cli.ParseTimecode(o.desde); err != nil {
			return config{}, i18n.Errorf("--from: %w", err)
		}
	}
	if o.hasta != "" {
		if to, err = cli.ParseTimecode(o.hasta); err != nil {
			return config{}, fmt.Errorf("--to: %w", err)
		}
		if to <= from {
			return config{}, i18n.Errorf("--to tiene que ser posterior a --from")
		}
	}
	salida := o.salida
	if salida == "" {
		stem := strings.TrimSuffix(filepath.Base(entrada), filepath.Ext(entrada))
		salida = filepath.Join(filepath.Dir(entrada), fmt.Sprintf("%s-%s.mp4", stem, strings.ReplaceAll(tui.Bytes(target), " ", "")))
	}
	if !strings.EqualFold(filepath.Ext(salida), ".mp4") {
		return config{}, i18n.Errorf("la salida tiene que ser .mp4 (vino %q)", salida)
	}
	if fsx.SameFile(entrada, salida) {
		return config{}, i18n.Errorf("la salida no puede ser el mismo archivo que la entrada")
	}
	if !o.forzar && fsx.Exists(salida) {
		return config{}, i18n.Errorf("%s ya existe (usá --force para sobrescribirlo)", fsx.Display(salida))
	}
	return config{
		entrada:  entrada,
		salida:   salida,
		preset:   o.preset,
		etiqueta: etiqueta,
		plan: squash.Options{
			Target: target, Codec: squash.Codec(o.codec), NoAudio: o.sinAudio,
			KeepFPS: o.mantener, MaxShort: o.maxRes, From: from, To: to,
		},
	}, nil
}

// bloqueReceta muestra de dónde se parte, a dónde se va y cómo.
func bloqueReceta(t *tui.Term, m *ffx.Media, p squash.Plan, cfg config) []string {
	v := m.Video
	origen := i18n.F("%s · %d×%d · %s fps · %s · %s", filepath.Base(cfg.entrada), v.Width, v.Height,
		fpsStr(v.FPS), tui.Clock(m.Duration), tui.Bytes(m.Size))
	if v.HDR() {
		origen += " · HDR"
	}
	audio := i18n.T("sin audio")
	if p.AudioKbps > 0 {
		ch := i18n.T("estéreo")
		if p.AudioChannels == 1 {
			ch = "mono"
		}
		audio = i18n.F("%d kb/s de audio %s", p.AudioKbps, ch)
	}
	objetivo := i18n.F("%s  →  %s de video + %s", cfg.etiqueta, mbps(p.VideoKbps), audio)
	if cfg.plan.From > 0 || cfg.plan.To > 0 {
		objetivo += i18n.F("  ·  tramo de %s", tui.Clock(p.Duration))
	}
	receta := i18n.F("%d×%d a %s fps · %s %s · dos pasadas", p.Width, p.Height, fpsStr(p.FPS), strings.ToUpper(string(p.Codec)), cfg.preset)
	items := []tui.KV{
		{Key: i18n.T("origen"), Value: origen},
		{Key: i18n.T("objetivo"), Value: objetivo, Color: tui.Teal},
		{Key: i18n.T("receta"), Value: receta, Color: tui.Lavender},
	}
	out := append([]string{""}, t.KVBlock(items)...)
	// los motivos van alineados con los valores: la columna de rótulos mide lo que mida en el idioma
	ancho := 0
	for _, it := range items {
		ancho = max(ancho, tui.Width(it.Key))
	}
	pad := strings.Repeat(" ", ancho+3)
	for _, r := range p.Reasons {
		out = append(out, tui.Margin+pad+t.Paint(tui.Faint, "· "+r))
	}
	return append(out, "")
}

// estado es lo que comparte el lector de progreso de ffmpeg con el dibujo.
type estado struct {
	mu    sync.Mutex
	fase  squash.Phase
	desde time.Time
}

func codificar(ctx context.Context, t *tui.Term, tools ffx.Tools, p squash.Plan, cfg config, m *ffx.Media) (squash.Result, error) {
	st := &estado{desde: time.Now()}
	live := t.StartLive(func(width int) []string { return dibujarFase(t, st, p, width) })
	ultimoPaso := 0
	res, err := squash.Encode(ctx, tools, p, squash.EncodeOptions{
		Input: cfg.entrada, Output: cfg.salida, From: cfg.plan.From, Preset: cfg.preset, Video: m.Video,
	}, func(ph squash.Phase) {
		st.mu.Lock()
		cambio := ph.Step != ultimoPaso
		if cambio && ultimoPaso != 0 {
			live.Println(t.Status(tui.OK, st.fase.Name, tui.Duration(time.Since(st.desde))))
			st.desde = time.Now()
		}
		ultimoPaso = ph.Step
		st.fase = ph
		st.mu.Unlock()
		t.TaskProgress(1, ph.Frac)
	})
	st.mu.Lock()
	if err == nil && ultimoPaso != 0 {
		live.Println(t.Status(tui.OK, st.fase.Name, tui.Duration(time.Since(st.desde))))
	}
	st.mu.Unlock()
	live.Stop(false)
	t.TaskProgressClear()
	return res, err
}

func dibujarFase(t *tui.Term, st *estado, p squash.Plan, width int) []string {
	st.mu.Lock()
	ph := st.fase
	st.mu.Unlock()
	inner := width - len(tui.Margin)*2
	nombre := ph.Name
	if nombre == "" {
		nombre = i18n.T("Preparando…")
	}
	head := t.Paint(tui.Lavender, tui.Spinner(time.Now())+" "+nombre)
	var info []string
	if ph.Prog.OutTime > 0 {
		info = append(info, tui.Clock(ph.Prog.OutTime)+" / "+tui.Clock(p.Duration))
	}
	if ph.Prog.Speed > 0 {
		info = append(info, fmt.Sprintf("%s×", tui.Fixed(ph.Prog.Speed, 1)))
		if rest := p.Duration - ph.Prog.OutTime; rest > 0 {
			info = append(info, i18n.F("faltan %s", tui.Clock(time.Duration(float64(rest)/ph.Prog.Speed))))
		}
	}
	right := t.Paint(tui.Subtle, strings.Join(info, "   "))
	gap := max(1, inner-tui.Width(head)-tui.Width(right))
	line1 := tui.Margin + head + strings.Repeat(" ", gap) + right

	extra := ""
	if ph.Prog.Size > 0 {
		extra = "   " + i18n.F("%s de %s", tui.Bytes(ph.Prog.Size), tui.Bytes(p.Target))
	}
	pct := fmt.Sprintf(" %3.0f%%", ph.Frac*100)
	barW := max(10, inner-len(pct)-tui.Width(extra)-1)
	line2 := tui.Margin + t.Bar(ph.Frac, barW) + t.Paint(tui.Subtle, pct) + t.Paint(tui.Faint, extra)
	return []string{line1, line2}
}

func medir(ctx context.Context, t *tui.Term, tools ffx.Tools, p squash.Plan, cfg config, m *ffx.Media) (squash.Quality, error) {
	live := t.StartLive(func(width int) []string {
		return []string{tui.Margin + t.Paint(tui.Lavender, tui.Spinner(time.Now())+" "+i18n.T("Midiendo la calidad contra el original (VMAF sobre muestras)…"))}
	})
	defer live.Stop(false)
	return squash.Measure(ctx, tools, p, cfg.entrada, cfg.plan.From, cfg.salida, m.Video)
}

func tarjeta(t *tui.Term, m *ffx.Media, p squash.Plan, r squash.Result, q *squash.Quality, cfg config, d time.Duration) []string {
	uso := float64(r.Size) / float64(p.Target)
	items := []tui.Item{
		{Label: i18n.T("Tamaño final"), Value: i18n.F("%s  (%s del objetivo)", tui.Bytes(r.Size), tui.Percent(uso, 0)), Dot: tui.Sage},
	}
	if m.Size > 0 {
		items = append(items, tui.Item{Label: i18n.T("Original"), Value: fmt.Sprintf("%s  (−%s)", tui.Bytes(m.Size), tui.Percent(1-float64(r.Size)/float64(m.Size), 0)), Dot: tui.Sky})
	}
	if q != nil {
		items = append(items, tui.Item{Label: i18n.T("Calidad"), Value: fmt.Sprintf("%s %s · %s", q.Metric, tui.Fixed(q.Score, qDecimals(q.Metric)), q.Label()), Dot: tui.Lavender})
	}
	items = append(items,
		tui.Item{Label: i18n.T("Video"), Value: fmt.Sprintf("%s · %d×%d @ %s", mbps(r.VideoKbps), p.Width, p.Height, fpsStr(p.FPS)), Dot: tui.Teal},
	)
	if p.AudioKbps > 0 {
		items = append(items, tui.Item{Label: i18n.T("Audio"), Value: fmt.Sprintf("%d kb/s", p.AudioKbps), Dot: tui.Peach})
	}
	if len(r.Attempts) > 1 {
		items = append(items, tui.Item{Label: i18n.T("Ajustes"), Value: i18n.Nf(len(r.Attempts)-1, "%s corrección", "%s correcciones", tui.Int(int64(len(r.Attempts)-1))), Dot: tui.Cream})
	}
	items = append(items,
		tui.Item{Label: i18n.T("Archivo"), Value: fsx.Display(cfg.salida), Dot: tui.Pink},
		tui.Item{Label: i18n.T("Tiempo"), Value: tui.Duration(d), Dot: tui.Subtle},
	)
	return t.Card("vidsquash", items)
}

func qDecimals(metric string) int {
	if metric == "SSIM" {
		return 4
	}
	return 1
}

func mbps(k int) string {
	if k >= 1000 {
		return tui.Fixed(float64(k)/1000, 2) + " Mb/s"
	}
	return tui.Int(int64(k)) + " kb/s"
}

func fpsStr(f float64) string {
	if f == float64(int64(f)) {
		return tui.Int(int64(f))
	}
	return tui.Fixed(f, 2)
}

func fallo(t *tui.Term, err error) int {
	t.Blank()
	t.Lines(t.Marked("✗ ", tui.Rose, err.Error()))
	t.Blank()
	return cli.ExitFailure
}
