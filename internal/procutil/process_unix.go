//go:build !windows

package procutil

import (
	"os"
	"syscall"
)

func terminate(proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}

func kill(proc *os.Process) error {
	return proc.Kill()
}
