//go:build !unix

package bridge

import "os/exec"

// Isolate is a no-op where process groups are unavailable.
func Isolate(*exec.Cmd) {}

// KillTree kills the direct child.
func KillTree(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
