//go:build windows

package procutil

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func terminate(proc *os.Process) error {
	return killTree(proc.Pid)
}

func kill(proc *os.Process) error {
	return killTree(proc.Pid)
}

func killTree(pid int) error {
	cmd := exec.Command("taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("taskkill process tree %d: %w", pid, err)
	}
	return fmt.Errorf("taskkill process tree %d: %w: %s", pid, err, detail)
}
