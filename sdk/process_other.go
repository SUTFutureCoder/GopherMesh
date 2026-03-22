//go:build !windows

package mesh

import "os/exec"

func configureManagedProcess(cmd *exec.Cmd) {}

func registerManagedProcess(cmd *exec.Cmd) error {
	return nil
}
