//go:build !windows

package parentwatch

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"
)

type unixMonitor struct {
	pid      int
	interval time.Duration
	dead     bool
}

func newMonitor(pid int, interval time.Duration) (Monitor, error) {
	alive, err := processAlive(pid)
	if err != nil {
		return nil, err
	}
	return &unixMonitor{pid: pid, interval: interval, dead: !alive}, nil
}

func (m *unixMonitor) Wait(ctx context.Context) error {
	if m.dead {
		return nil
	}

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			alive, err := processAlive(m.pid)
			if err != nil {
				return err
			}
			if !alive {
				return nil
			}
		}
	}
}

func (m *unixMonitor) Close() error {
	return nil
}

func processAlive(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EPERM):
		// The process exists, but the current user is not allowed to signal it.
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, fmt.Errorf("check parent process %d: %w", pid, err)
	}
}
