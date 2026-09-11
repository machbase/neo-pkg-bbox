//go:build !linux

package mediamtx

import "os/exec"

func setPdeathsig(_ *exec.Cmd) {}
