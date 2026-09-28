//go:build windows

package ffx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// `winget install Gyan.FFmpeg` deja ffmpeg en WinGet\Packages y agrega su bin
// al PATH del usuario, que una terminal abierta antes de instalar no ve (y
// WinGet\Links queda vacío). Locate lo encuentra igual.
func TestLocateEnElPaqueteDeWinget(t *testing.T) {
	la := t.TempDir()
	t.Setenv("LOCALAPPDATA", la)
	t.Setenv("PATH", t.TempDir()) // un PATH sin ffmpeg

	if _, err := Locate(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sin nada instalado, Locate tiene que dar ErrNotFound (dio %v)", err)
	}

	bin := filepath.Join(la, "Microsoft", "WinGet", "Packages",
		"Gyan.FFmpeg_Microsoft.Winget.Source_8wekyb3d8bbwe", "ffmpeg-9.0.2-full_build", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ffmpeg.exe", "ffprobe.exe"} {
		if err := os.WriteFile(filepath.Join(bin, n), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Locate()
	if err != nil {
		t.Fatalf("con el paquete de winget: %v", err)
	}
	if got.FFmpeg != filepath.Join(bin, "ffmpeg.exe") || got.FFprobe != filepath.Join(bin, "ffprobe.exe") {
		t.Errorf("Locate = %+v, esperaba los dos en %s", got, bin)
	}
}
