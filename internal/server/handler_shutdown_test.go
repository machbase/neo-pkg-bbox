package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHandlerShutdownWaitsForCameraLoop(t *testing.T) {
	done := make(chan struct{})
	cancelled := make(chan struct{}, 1)
	h := &Handler{
		processes: map[string]*cameraProcess{
			"camera-1": {
				cancel: func() {
					cancelled <- struct{}{}
					go func() {
						time.Sleep(50 * time.Millisecond)
						close(done)
					}()
				},
				done: done,
			},
		},
	}

	started := time.Now()
	h.Shutdown()

	select {
	case <-cancelled:
	default:
		t.Fatal("camera loop was not cancelled")
	}
	assert.True(t, h.shuttingDown)
	assert.GreaterOrEqual(t, time.Since(started), 50*time.Millisecond)
}
