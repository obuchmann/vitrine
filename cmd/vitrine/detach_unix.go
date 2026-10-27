//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detach puts the daemon in its own session, so it outlives the agent that
// started vitrine mcp and is not hit by signals to its process group.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
