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
