//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
)

// A user unit inherits the user manager's environment, not Arkey's, so the
// configured environment has to be set on the unit itself.
func TestSystemdServiceSetsConfiguredEnvironment(t *testing.T) {
	r := &recordingRunner{}
	s := SystemdService{Runner: r}
	pid, err := s.StartConfigured(context.Background(), "arkey-llama", []string{"/opt/llama/bin/llama-server", "--model", "/m/a.gguf"}, "/tmp/llama.log", map[string]string{"LD_LIBRARY_PATH": "/opt/llama/bin", "A": "1"})
	if err != nil || pid != 17 {
		t.Fatalf("pid=%d err=%v", pid, err)
	}
	args := strings.Join(r.commands[0], " ")
	want := "systemd-run --user --quiet --collect --unit=arkey-llama"
	if !strings.HasPrefix(args, want) || !strings.Contains(args, "--setenv=A=1 --setenv=LD_LIBRARY_PATH=/opt/llama/bin -- /opt/llama/bin/llama-server --model /m/a.gguf") {
		t.Fatalf("unexpected systemd-run argv: %s", args)
	}
}
