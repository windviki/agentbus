//go:build !linux

// Package procinfo: non-Linux fallback. Sessions fall back to TTL-based
// liveness (no pid recorded) and the "unknown" agent label.
package procinfo

type Agent struct {
	Pid   int
	Start string
	Label string
}

func Detect() Agent { return Agent{} }

func Alive(pid int, start string) bool { return false }
