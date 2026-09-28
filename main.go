// vidsquash comprime un video para que entre en un tamaño máximo (el límite de
// Discord, WhatsApp o un mail) con la mejor calidad posible. Usa ffmpeg.
package main

import (
	"os"

	"github.com/agustinyarrus/vidsquash/internal/tui"
	"github.com/agustinyarrus/vidsquash/internal/version"
	"github.com/agustinyarrus/vidsquash/internal/vidsquash"
)

func main() {
	t := tui.Open()
	defer t.Close()
	os.Exit(vidsquash.Main(t, version.String(), os.Args[1:]))
}
