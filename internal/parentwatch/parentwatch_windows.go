//go:build windows

package parentwatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

type windowsMonitor struct {
	handle   windows.Handle
	interval time.Duration
	dead     bool
}

func newMonitor(pid int, interval time.Duration) (Monitor, error) {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return &windowsMonitor{dead: true, interval: interval}, nil
		}
		return nil, fmt.Errorf("open parent process %d: %w", pid, err)
	}
	return &windowsMonitor{handle: handle, interval: interval}, nil
}

func (m *windowsMonitor) Wait(ctx context.Context) error {
	if m.dead {
		return nil
	}

	waitMillis := uint32(m.interval / time.Millisecond)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		result, err := windows.WaitForSingleObject(m.handle, waitMillis)
		if err != nil {
			return fmt.Errorf("wait for parent process: %w", err)
		}
		switch result {
		case windows.WAIT_OBJECT_0:
			return nil
		case uint32(windows.WAIT_TIMEOUT):
			continue
		default:
			return fmt.Errorf("wait for parent process returned status 0x%x", result)
		}
	}
}

func (m *windowsMonitor) Close() error {
	if m.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(m.handle)
	m.handle = 0
	return err
}
