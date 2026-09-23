//go:build windows

package ffx

import (
	"os/exec"
	"syscall"
)

// hideWindow evita que ffmpeg abra una consola propia cuando la herramienta
// corre sin consola (lanzada desde un acceso directo o una tarea).
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
