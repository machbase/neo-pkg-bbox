package parentwatch

import (
	"context"
	"fmt"
	"time"
)

const checkInterval = time.Second

// Monitor waits for a specific process to exit. Implementations bind to the
// process that owns pid when New is called whenever the OS provides that
// facility, so a later PID reuse does not keep neo-blackbox alive.
type Monitor interface {
	Wait(context.Context) error
	Close() error
}

// New creates a parent process monitor. A parent PID is only required when
// neo-blackbox is launched by a supervisor; standalone execution does not call
// this function.
func New(pid int) (Monitor, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("parent PID must be positive: %d", pid)
	}
	return newMonitor(pid, checkInterval)
}
