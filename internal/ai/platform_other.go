//go:build !linux

package ai

import "os/exec"

func setPdeathsig(_ *exec.Cmd) {}
