package parentwatch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestNewRejectsInvalidPID(t *testing.T) {
	for _, pid := range []int{-1, 0} {
		if _, err := New(pid); err == nil {
			t.Fatalf("New(%d) succeeded, want error", pid)
		}
	}
}

func TestMonitorStopsWhenContextIsCanceled(t *testing.T) {
	monitor, err := New(os.Getpid())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer monitor.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := monitor.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context.Canceled", err)
	}
}

func TestMonitorDetectsProcessExit(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestParentWatchHelperProcess$")
	cmd.Env = append(os.Environ(), "PARENTWATCH_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}

	monitor, err := New(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("New: %v", err)
	}
	defer monitor.Close()

	done := make(chan error, 1)
	go func() {
		done <- monitor.Wait(context.Background())
	}()

	select {
	case err := <-done:
		t.Fatalf("Wait returned before helper exited: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("helper exited successfully after Kill, want signal error")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("parent exit was not detected within 3 seconds")
	}
}

func TestParentWatchHelperProcess(t *testing.T) {
	if os.Getenv("PARENTWATCH_HELPER") != "1" {
		return
	}
	select {}
}
