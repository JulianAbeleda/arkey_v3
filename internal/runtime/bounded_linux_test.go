//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
)

type recordingRunner struct{ commands [][]string }

func (r *recordingRunner) Run(_ context.Context, cmd string, args ...string) ([]byte, error) {
	r.commands = append(r.commands, append([]string{cmd}, args...))
	if strings.Contains(strings.Join(args, " "), "--property=MainPID") {
		return []byte("17\n"), nil
	}
	return nil, nil
}
func TestBoundedServiceContainsWholeProcessTree(t *testing.T) {
	r := &recordingRunner{}
	s := BoundedService{Runner: r, MemoryBytes: 8 << 30}
	pid, err := s.StartConfigured(context.Background(), "test-unit", []string{"/venv/bin/python", "-m", "vllm.entrypoints.openai.api_server"}, "/tmp/server.log", map[string]string{"BOLTBEAM_REQUIRE_PREBUILT": "1"})
	if err != nil || pid != 17 {
		t.Fatalf("pid=%d err=%v", pid, err)
	}
	args := strings.Join(r.commands[0], " ")
	for _, want := range []string{"sudo -n systemd-run", "--property=MemoryMax=8589934592", "--property=MemorySwapMax=0", "--property=KillMode=control-group", "--property=OOMPolicy=stop", "--property=Restart=no", "--setenv=BOLTBEAM_REQUIRE_PREBUILT=1", "-- /venv/bin/python"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(args, "--user") {
		t.Fatal("heavy process escaped into user-session cgroup")
	}
}
func TestBoundedServiceRefusesMissingMemoryCap(t *testing.T) {
	r := &recordingRunner{}
	s := BoundedService{Runner: r}
	if _, err := s.Start(context.Background(), "test", nil, "/tmp/log"); err == nil {
		t.Fatal("unbounded launch accepted")
	}
	if len(r.commands) != 0 {
		t.Fatal("started without memory cap")
	}
}
