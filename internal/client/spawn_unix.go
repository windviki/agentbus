//go:build unix

package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// EnsureRunning spawns the daemon as a detached child if it is not already
// listening, guarded by a flock so concurrent first-callers don't race.
func EnsureRunning(home string) error {
	if c, err := dial(home); err == nil {
		c.Close()
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	lockPath := filepath.Join(home, "spawn.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	if c, err := dial(home); err == nil {
		c.Close()
		return nil // someone else won the race
	}
	logf, err := os.OpenFile(filepath.Join(home, "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "serve")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	for i := 0; i < 80; i++ {
		if c, err := dial(home); err == nil {
			c.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not come up; see %s", filepath.Join(home, "daemon.log"))
}
