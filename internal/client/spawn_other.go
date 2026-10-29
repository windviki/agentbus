//go:build !unix

package client

import "fmt"

// EnsureRunning is only implemented on unix platforms; elsewhere the user
// runs "agentbus serve" in the foreground.
func EnsureRunning(home string) error {
	return fmt.Errorf("auto-start unsupported on this platform; run: agentbus serve")
}
