package squash

// Tests de la corrección de tamaño: la secante con salvaguardas de nextBitrate
// y la decisión de nextAttempt, con intentos armados a mano, con codificadores
// simulados y, al final, con ffmpeg de verdad.

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/agustinyarrus/vidsquash/internal/ffx"
)

// Con un intento corto, la corrección es la proporción sobre la parte de video.
func TestCorreccionProporcional(t *testing.T) {
	const fijo, objetivo = 10_000, 10_000_000
	intentos := []Attempt{{VideoKbps: 1000, Size: 6_010_000}} // 6 MB de video
	next, again := nextAttempt(intentos, objetivo, fijo)
	// (0,985 × 10 MB − 10 KB) / 6 MB × 1000 kb/s = 1640 kb/s
	if !again || next != 1640 {
		t.Errorf("próximo intento %d kb/s (sigue: %v), esperaba 1640", next, again)
	}
}

// Sin un intento del otro lado del objetivo, una corrección a lo sumo duplica
// el bitrate o lo divide por dos, por lejos que parezca estar.
func TestCorreccionAcotada(t *testing.T) {
	const fijo = 10_000
	if got := nextBitrate([]Attempt{{VideoKbps: 1000, Size: 1_010_000}}, 9_850_000, fijo); got != 2000 {
		t.Errorf("hacia arriba: %d kb/s, esperaba el tope de 2000", got)
	}
	if got := nextBitrate([]Attempt{{VideoKbps: 1000, Size: 40_010_000}}, 9_850_000, fijo); got != 500 {
		t.Errorf("hacia abajo: %d kb/s, esperaba el piso de 500", got)
	}
}

// El caso real que rompía Encode (un clip HDR de 2 s a 640×360 con 2 MB de
// objetivo): x264 ya no tenía en qué gastar bits, el tamaño no se movió y la
// secante pedía 28.620 Mb/s, después 117 millones, y libx264 no abría. Ahora se
// detiene y se queda con lo que entra.
func TestCorreccionSaturada(t *testing.T) {
	intentos := []Attempt{{VideoKbps: 7820, Size: 1_259_386}, {VideoKbps: 12250, Size: 1_259_496}}
	if next, again := nextAttempt(intentos, 2_000_000, 4816); again {
		t.Errorf("el codificador está saturado y la corrección pide otro intento a %d kb/s", next)
	}
}

// Con un intento que entra y otro que se pasa, la respuesta está entre los dos
// (regula falsi), y si el ruido del codificador dio vuelta la pendiente, en el
// medio (bisección).
func TestCorreccionEntreDosIntentos(t *testing.T) {
	const want = 9_850_000
	regula := nextBitrate([]Attempt{{VideoKbps: 1000, Size: 8_000_000}, {VideoKbps: 2000, Size: 12_000_000}}, want, 0)
	if regula != 1462 { // 1000 + (9,85 − 8) / (12 − 8) × 1000
		t.Errorf("regula falsi: %d kb/s, esperaba 1462", regula)
	}
	biseccion := nextBitrate([]Attempt{{VideoKbps: 2000, Size: 9_000_000}, {VideoKbps: 1500, Size: 10_500_000}}, want, 0)
	if biseccion != 1750 {
		t.Errorf("pendiente al revés: %d kb/s, esperaba el medio, 1750", biseccion)
	}
}

// Un bitrate que ya se probó no se vuelve a codificar: daría el mismo archivo.
func TestCorreccionNoRepite(t *testing.T) {
	intentos := []Attempt{{VideoKbps: 1000, Size: 9_000_000}, {VideoKbps: 1001, Size: 10_500_000}}
	if next, again := nextAttempt(intentos, 10_000_000, 0); again {
		t.Errorf("volvería a codificar a %d kb/s, que ya se probó", next)
	}
}

// Propiedades del bucle completo contra codificadores simulados, de exactos a
// saturados, en 9 combinaciones de objetivo y duración cada uno:
//   - ninguna corrección más que duplica el mayor bitrate probado;
//   - nunca más de maxCorrections+1 segundas pasadas;
//   - si el objetivo es alcanzable, termina entre el 90 % y el 100 %;
//   - si el codificador satura antes, siempre entrega algo que entra y deja de
//     corregir en cuanto lo nota (a lo sumo dos correcciones).
func TestCorreccionConCodificadoresSimulados(t *testing.T) {
	const fijo = 20_000.0
	curvas := []struct {
		nombre     string
		enc        func(bytesPorKbps, objetivo float64) simulado
		alcanzable bool
	}{
		{"exacto", func(b, _ float64) simulado { return simulado{fijo: fijo, bytesPorKbps: b} }, true},
		{"se pasa un 8 %", func(b, _ float64) simulado { return simulado{fijo: fijo, bytesPorKbps: b * 1.08} }, true},
		{"se queda un 25 % corto", func(b, _ float64) simulado { return simulado{fijo: fijo, bytesPorKbps: b * 0.75} }, true},
		{"con ruido de ±4 %", func(b, _ float64) simulado { return simulado{fijo: fijo, bytesPorKbps: b, ruido: 0.04} }, true},
		{"se satura cerca del 60 %", func(b, obj float64) simulado {
			return simulado{fijo: fijo, bytesPorKbps: b, techo: 0.6 * obj}
		}, false},
		{"techo duro al 40 %", func(b, obj float64) simulado {
			return simulado{fijo: fijo, bytesPorKbps: b, techo: 0.4 * obj, duro: true}
		}, false},
	}
	for _, c := range curvas {
		for _, objetivo := range []int64{8e6, 25e6, 100e6} {
			for _, secs := range []float64{10, 60, 600} {
				t.Run(fmt.Sprintf("%s/%s/%.0fs", c.nombre, humanBytes(objetivo), secs), func(t *testing.T) {
					bytesPorKbps := 1000.0 / 8 * secs
					enc := c.enc(bytesPorKbps, float64(objetivo))
					inicial := int((0.98*float64(objetivo) - fijo) / bytesPorKbps) // lo que planea MakePlan
					intentos, mejor := simularCorreccion(enc, objetivo, inicial)

					for i := 1; i < len(intentos); i++ {
						mayor := 0
						for _, a := range intentos[:i] {
							mayor = max(mayor, a.VideoKbps)
						}
						if k := intentos[i].VideoKbps; k < minVideoKbps || float64(k) > maxStepUp*float64(mayor) {
							t.Errorf("intento %d a %d kb/s: fuera de [%d, %.0f]: %v", i, k, minVideoKbps, maxStepUp*float64(mayor), intentos)
						}
					}
					if len(intentos) > maxCorrections+1 {
						t.Errorf("%d segundas pasadas: %v", len(intentos), intentos)
					}
					switch {
					case mejor < 0:
						t.Fatalf("ningún intento entra en el objetivo: %v", intentos)
					case c.alcanzable && float64(intentos[mejor].Size) < acceptLowFrac*float64(objetivo):
						t.Errorf("terminó en el %.1f %% del objetivo: %v", 100*float64(intentos[mejor].Size)/float64(objetivo), intentos)
					case !c.alcanzable && len(intentos) > 3:
						t.Errorf("siguió corrigiendo con el codificador saturado: %v", intentos)
					}
				})
			}
		}
	}
}

// simulado responde tamaño(bitrate) como un codificador de dos pasadas: lo
// fijo del contenedor más un video proporcional al bitrate, con un ruido
// determinista y, si tiene techo, saturado (suave o de golpe) como x264 cuando
// el contenido no tiene en qué gastar más bits.
type simulado struct {
	fijo, bytesPorKbps float64
	ruido              float64 // amplitud del error relativo, que cambia con el bitrate
	techo              float64 // bytes de video máximos (0 = sin techo)
	duro               bool    // techo de golpe en vez de exponencial
}

func (s simulado) tamano(kbps int) int64 {
	v := float64(kbps) * s.bytesPorKbps * (1 + s.ruido*math.Sin(float64(kbps)))
	switch {
	case s.techo > 0 && s.duro:
		v = min(v, s.techo)
	case s.techo > 0:
		v = s.techo * (1 - math.Exp(-v/s.techo))
	}
	return int64(s.fijo + v)
}

// simularCorreccion corre el bucle de Encode contra el simulado y devuelve los
// intentos y el índice del mejor (el más grande que entra), o -1.
func simularCorreccion(enc simulado, objetivo int64, inicial int) ([]Attempt, int) {
	var intentos []Attempt
	mejor := -1
	for vk, sigue := inicial, true; sigue; {
		intentos = append(intentos, Attempt{VideoKbps: vk, Size: enc.tamano(vk)})
		if i := len(intentos) - 1; intentos[i].Size <= objetivo && (mejor < 0 || intentos[i].Size > intentos[mejor].Size) {
			mejor = i
		}
		vk, sigue = nextAttempt(intentos, objetivo, enc.fijo)
	}
	return intentos, mejor
}

// De punta a punta con ffmpeg: un clip simple con muchísimo presupuesto satura
// a x264. Encode tiene que entregar el archivo, no fallar pidiéndole a libx264
// un bitrate imposible.
func TestEncodeSaturadoEntrega(t *testing.T) {
	if testing.Short() {
		t.Skip("codifica un video de verdad")
	}
	tools, err := ffx.Locate()
	if err != nil {
		t.Skip(err)
	}
	if motivo := faltaEncoder(tools, "libx264"); motivo != "" {
		t.Skip(motivo)
	}
	orig := filepath.Join(t.TempDir(), "simple.mkv")
	correr(t, tools, "-f", "lavfi", "-i", "testsrc2=s=320x180:r=30:d=2", "-c:v", "ffv1", "-y", orig)
	m, err := tools.Probe(t.Context(), orig)
	if err != nil {
		t.Fatal(err)
	}
	p, err := MakePlan(m, Options{Target: 2e6})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Encode(t.Context(), tools, p, EncodeOptions{Input: orig, Output: filepath.Join(t.TempDir(), "salida.mp4"), Preset: "veryfast", Video: m.Video}, nil)
	if err != nil {
		t.Fatalf("Encode falló con el codificador saturado: %v", err)
	}
	t.Logf("plan %d kb/s → %s en %d segundas pasadas: %v", p.VideoKbps, humanBytes(res.Size), len(res.Attempts), res.Attempts)
	if res.Size > p.Target || len(res.Attempts) > 3 {
		t.Errorf("%s en %d intentos (objetivo %s): %v", humanBytes(res.Size), len(res.Attempts), humanBytes(p.Target), res.Attempts)
	}
}
