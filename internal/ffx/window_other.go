//go:build !windows

package ffx

import "os/exec"

func hideWindow(*exec.Cmd) {}
