package tui

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// La capa de consola es liviana a propósito: no carga red (net, net/netip)
// ni lanzar procesos (os/exec). vidsquash sí lanza ffmpeg, pero eso vive en
// ffx; si un cambio en tui arrastra esos paquetes, este test lo dice.
func TestSinPaquetesPesados(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: sin correr go list")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go no está en el PATH")
	}
	out, err := exec.Command("go", "list", "-deps", "github.com/agustinyarrus/vidsquash/internal/tui").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	deps := strings.Fields(string(out))
	for _, no := range []string{"net", "net/netip", "os/exec"} {
		if slices.Contains(deps, no) {
			t.Errorf("tui depende de %s", no)
		}
	}
}
