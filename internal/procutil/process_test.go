package procutil

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestStopProcess(t *testing.T) {
	tests := []struct {
		name string
		stop func(*os.Process) error
	}{
		{name: "terminate", stop: Terminate},
		{name: "kill", stop: Kill},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$")
			cmd.Env = append(os.Environ(), "PROCUTIL_HELPER=1")
			if err := cmd.Start(); err != nil {
				t.Fatalf("start helper: %v", err)
			}

			exited := make(chan error, 1)
			go func() {
				exited <- cmd.Wait()
			}()

			if err := tc.stop(cmd.Process); err != nil {
				t.Fatalf("stop helper: %v", err)
			}

			select {
			case <-exited:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatal("helper did not exit within 3 seconds")
			}
		})
	}
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv("PROCUTIL_HELPER") != "1" {
		return
	}
	select {}
}
