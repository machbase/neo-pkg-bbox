package procutil

import "os"

// Terminate asks a process to stop. On Windows there is no generic SIGTERM,
// so the whole process tree is terminated instead.
func Terminate(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return terminate(proc)
}

// Kill forcefully stops a process and, on Windows, all of its descendants.
func Kill(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return kill(proc)
}
