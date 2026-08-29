//go:build windows

package acp

import (
	"os/exec"
	"syscall"
)

// configureBackgroundProcess prevents console-based agents from creating a
// visible console window when launched by the GUI app.
func configureBackgroundProcess(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
