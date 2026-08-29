//go:build !windows

package acp

import "os/exec"

func configureBackgroundProcess(cmd *exec.Cmd) {}
