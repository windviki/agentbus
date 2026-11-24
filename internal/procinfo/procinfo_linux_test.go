//go:build linux

package procinfo

import "testing"

func TestAgentbusWrapper(t *testing.T) {
	cases := map[string]bool{
		"node /tmp/prefix/bin/agentbus register --session sid:a":                           true,
		"node /tmp/prefix/lib/node_modules/@aixyzstudio/agentbus-cli/bin/agentbus.js hook": true,
		"/home/coder/.local/bin/agentbus hook":                                             true,
		"node /home/coder/agentbus/test/native-push.mjs":                                   false,
		"/opt/zcode/zcode.cjs --session abc":                                               false,
		"kimi --prompt test":                                                               false,
	}
	for cmdline, want := range cases {
		if got := agentbusWrapper(cmdline); got != want {
			t.Errorf("agentbusWrapper(%q) = %v, want %v", cmdline, got, want)
		}
	}
}
