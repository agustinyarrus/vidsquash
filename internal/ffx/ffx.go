// Package ffx maneja ffmpeg y ffprobe como procesos externos: los encuentra,
// los corre con cancelación (Ctrl+C mata el proceso y no deja basura) y lee el
// progreso en vivo que ffmpeg escribe con -progress.
package ffx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNotFound se devuelve cuando no hay ffmpeg/ffprobe en el sistema.
var ErrNotFound = errors.New("no encontré ffmpeg (instalalo con: winget install Gyan.FFmpeg)")

// Tools son las rutas de los ejecutables.
type Tools struct {
	FFmpeg  string
	FFprobe string
}

// Locate busca ffmpeg y ffprobe: primero junto al exe de la herramienta (para
// una instalación portable), después en el PATH, después en los lugares donde
// los deja winget.
func Locate() (Tools, error) {
	find := func(name string) string {
		if exe, err := os.Executable(); err == nil {
			p := filepath.Join(filepath.Dir(exe), name+".exe")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			p := filepath.Join(la, "Microsoft", "WinGet", "Links", name+".exe")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		return ""
	}
	t := Tools{FFmpeg: find("ffmpeg"), FFprobe: find("ffprobe")}
	if t.FFmpeg == "" || t.FFprobe == "" {
		return t, ErrNotFound
	}
	return t, nil
}

// Progress es un cuadro del progreso que informa ffmpeg.
type Progress struct {
	OutTime time.Duration // posición en el video de salida
	Frame   int64
	FPS     float64
	Speed   float64 // múltiplo del tiempo real (1,5 = 1,5×)
	Size    int64   // bytes escritos hasta ahora (0 si la salida es nula)
	Done    bool
}

// Run corre ffmpeg con los argumentos dados, agregando -progress para leer el
// avance. Cancelar el contexto mata el proceso. El error, si lo hay, trae las
// últimas líneas de stderr (lo que ffmpeg dijo antes de fallar).
func (t Tools) Run(ctx context.Context, args []string, onProgress func(Progress)) error {
	full := append([]string{"-hide_banner", "-nostdin", "-nostats", "-loglevel", "error", "-progress", "pipe:1"}, args...)
	cmd := exec.CommandContext(ctx, t.FFmpeg, full...)
	hideWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var tail tailBuffer
	cmd.Stderr = &tail
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("no pude arrancar ffmpeg: %w", err)
	}
	parseProgress(stdout, onProgress)
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(tail.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ffmpeg falló: %s", lastLines(msg, 4))
	}
	return nil
}

// Output corre ffmpeg y devuelve todo stderr (para filtros que informan por
// ahí, como libvmaf con su "VMAF score").
func (t Tools) Output(ctx context.Context, args []string) (string, error) {
	full := append([]string{"-hide_banner", "-nostdin", "-nostats"}, args...)
	cmd := exec.CommandContext(ctx, t.FFmpeg, full...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return string(out), fmt.Errorf("ffmpeg falló: %s", lastLines(strings.TrimSpace(string(out)), 4))
	}
	return string(out), nil
}

// parseProgress lee los bloques clave=valor de -progress; cada bloque cierra
// con "progress=continue" o "progress=end".
func parseProgress(r io.Reader, onProgress func(Progress)) {
	sc := bufio.NewScanner(r)
	var p Progress
	for sc.Scan() {
		key, val, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		switch key {
		case "out_time_us", "out_time_ms": // las dos traen microsegundos (herencia de ffmpeg)
			if us, err := strconv.ParseInt(val, 10, 64); err == nil && us >= 0 {
				p.OutTime = time.Duration(us) * time.Microsecond
			}
		case "frame":
			p.Frame, _ = strconv.ParseInt(val, 10, 64)
		case "fps":
			p.FPS, _ = strconv.ParseFloat(val, 64)
		case "speed":
			p.Speed, _ = strconv.ParseFloat(strings.TrimSuffix(val, "x"), 64)
		case "total_size":
			p.Size, _ = strconv.ParseInt(val, 10, 64)
		case "progress":
			p.Done = val == "end"
			if onProgress != nil {
				onProgress(p)
			}
		}
	}
}

// tailBuffer guarda solo los últimos KB de stderr: ffmpeg puede escupir mucho
// y solo interesa el final para explicar un fallo.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailMax = 8 << 10

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailMax {
		t.buf = t.buf[len(t.buf)-tailMax:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " · ")
}
