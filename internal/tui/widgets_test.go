package tui

import (
	"strings"
	"testing"
)

// La cabecera lleva la versión a la derecha y con su "v" (v1.0.0), igual en
// la ayuda y en cada corrida. Si la ventana es tan angosta que no entra, se
// queda con el nombre y lo que hace, sin partir la línea.
func TestHeaderVersionALaDerecha(t *testing.T) {
	term := &Term{}
	term.SetWidth(100)
	h := term.Header("prueba", "hace algo útil", "1.0.0")
	if len(h) != 3 || h[0] != "" || h[2] != "" {
		t.Fatalf("cabecera = %q", h)
	}
	if !strings.HasPrefix(h[1], Margin+"prueba  ·  hace algo útil ") || !strings.HasSuffix(h[1], " v1.0.0") {
		t.Errorf("línea = %q", h[1])
	}
	if w := Width(h[1]); w != 100-len(Margin) {
		t.Errorf("ancho %d, esperaba %d (la versión pegada al margen derecho)", w, 100-len(Margin))
	}

	term.SetWidth(24)
	h = term.Header("prueba", "hace algo útil", "1.0.0")
	if len(h) != 3 || h[1] != Margin+"prueba  ·  hace algo útil" {
		t.Errorf("angosta: %q", h)
	}
}

// Un error que no entra en la ventana se parte en palabras, alineado después
// de la marca: antes iba en un renglón y la consola lo cortaba donde caía (el
// ")" de "--for discrod" quedaba solo abajo, a 100 columnas).
func TestMarkedSeParteEnPalabras(t *testing.T) {
	term := &Term{}
	term.SetWidth(100)
	msg := `--for: "discrod" no es una opción; ¿quisiste decir "discord"? (discord, whatsapp, outlook, gmail)`
	l := term.Marked("✗ ", Rose, msg)
	if len(l) != 2 || !strings.HasPrefix(l[0], Margin+"✗ --for:") || !strings.HasPrefix(l[1], Margin+"  ") {
		t.Fatalf("renglones = %q", l)
	}
	var palabras []string
	for _, x := range l {
		if w := Width(x); w > 100-len(Margin) {
			t.Errorf("renglón de %d columnas: %q", w, x)
		}
		palabras = append(palabras, strings.Fields(strings.TrimPrefix(strings.TrimSpace(x), "✗ "))...)
	}
	if strings.Join(palabras, " ") != msg {
		t.Errorf("el mensaje cambió al partirse: %q", palabras)
	}
	// Uno corto queda como siempre: margen, marca y texto.
	if l := term.Marked("✗ ", Rose, "falta el tamaño"); len(l) != 1 || l[0] != Margin+"✗ falta el tamaño" {
		t.Errorf("corto: %q", l)
	}
}
