package squash

import (
	"errors"
	"testing"
	"time"

	"github.com/agustinyarrus/vidsquash/internal/ffx"
)

func media(w, h int, fps float64, secs int, audio bool) *ffx.Media {
	m := &ffx.Media{Duration: time.Duration(secs) * time.Second, Video: &ffx.VideoStream{Width: w, Height: h, FPS: fps}}
	if audio {
		m.Audio = &ffx.AudioStream{Channels: 2, SampleRate: 48000, BitRate: 192000}
	}
	return m
}

// El plan nunca promete más de lo que entra: video + audio + contenedor ≤ objetivo.
func TestPlanCabeEnElObjetivo(t *testing.T) {
	for _, secs := range []int{5, 60, 600, 3600} {
		for _, target := range []int64{8e6, 25e6, 100e6} {
			p, err := MakePlan(media(1920, 1080, 60, secs, true), Options{Target: target})
			if errors.Is(err, ErrTooSmall) {
				continue
			}
			if err != nil {
				t.Fatalf("%d s, %d B: %v", secs, target, err)
			}
			total := int64(p.VideoKbps+p.AudioKbps)*1000/8*int64(secs) + p.Overhead
			if total > target {
				t.Errorf("%d s, %d B: el plan suma %d B, más que el objetivo", secs, target, total)
			}
		}
	}
}

// Con pocos bits por píxel, primero se bajan los fps (60 → 30) y después la resolución.
func TestPlanPrefiereResolucionAntesQueFPS(t *testing.T) {
	// 60 s de 1080p60 en 25 MB: ~3,2 Mb/s de video; a 1080p60 no alcanza, a 1080p30 sí.
	p, err := MakePlan(media(1920, 1080, 60, 60, true), Options{Target: 25e6})
	if err != nil {
		t.Fatal(err)
	}
	if p.Height != 1080 || p.FPS != 30 {
		t.Errorf("eligió %dx%d@%.0f, esperaba 1080p a 30 fps", p.Width, p.Height, p.FPS)
	}
}

// Un video vertical se escala por el lado CORTO.
func TestPlanVertical(t *testing.T) {
	p, err := MakePlan(media(1080, 1920, 30, 120, true), Options{Target: 10e6})
	if err != nil {
		t.Fatal(err)
	}
	if p.Width >= p.Height {
		t.Errorf("perdió la orientación vertical: %dx%d", p.Width, p.Height)
	}
	if p.Width%2 != 0 || p.Height%2 != 0 {
		t.Errorf("dimensiones impares %dx%d (yuv420p exige pares)", p.Width, p.Height)
	}
}

// Nunca se agranda un video chico, y con bits de sobra se conserva todo.
func TestPlanNoAgranda(t *testing.T) {
	p, err := MakePlan(media(640, 360, 25, 10, false), Options{Target: 50e6})
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != 640 || p.Height != 360 || p.FPS != 25 {
		t.Errorf("cambió %dx%d@%.0f, esperaba el original 640x360@25", p.Width, p.Height, p.FPS)
	}
}

// Un objetivo imposible da ErrTooSmall con una sugerencia, no un plan roto.
func TestPlanDemasiadoChico(t *testing.T) {
	_, err := MakePlan(media(1920, 1080, 30, 3600, true), Options{Target: 1e6})
	if !errors.Is(err, ErrTooSmall) {
		t.Fatalf("esperaba ErrTooSmall, vino %v", err)
	}
}

// Con pocos bits el audio pasa a mono, y nunca supera al original.
func TestAudio(t *testing.T) {
	if k, ch := chooseAudio(200, &ffx.AudioStream{Channels: 2, BitRate: 192000}, false); ch != 1 || k > 64 {
		t.Errorf("con 200 kb/s totales: %d kb/s %d canales, esperaba mono y ≤ 64", k, ch)
	}
	if k, _ := chooseAudio(5000, &ffx.AudioStream{Channels: 2, BitRate: 96000}, false); k != 96 {
		t.Errorf("no tendría que inflar el audio por encima de los 96 kb/s del original: %d", k)
	}
	if k, ch := chooseAudio(5000, &ffx.AudioStream{Channels: 6, BitRate: 640000}, false); ch != 2 || k != 160 {
		t.Errorf("5.1 con bits de sobra: %d kb/s %d canales, esperaba 160 en estéreo", k, ch)
	}
	if k, _ := chooseAudio(5000, nil, false); k != 0 {
		t.Error("sin pista de audio tendría que dar 0")
	}
}

// El recorte se respeta en la duración y en el presupuesto.
func TestPlanRecorte(t *testing.T) {
	p, err := MakePlan(media(1280, 720, 30, 600, true), Options{Target: 10e6, From: 60 * time.Second, To: 90 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if p.Duration != 30*time.Second {
		t.Errorf("duración %v, esperaba 30s", p.Duration)
	}
	if _, err := MakePlan(media(1280, 720, 30, 600, true), Options{Target: 10e6, From: 90 * time.Second, To: 60 * time.Second}); err == nil {
		t.Error("un recorte al revés tendría que fallar")
	}
}
