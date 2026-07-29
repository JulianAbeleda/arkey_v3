//go:build linux

package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/creack/pty"
)

func TestPTYHelper(t *testing.T) {
	if os.Getenv("ARKEY_PTY_HELPER") != "1" {
		return
	}
	if _, err := tea.NewProgram(New(NopServices{})).Run(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestPTYNavigationResizeAndTerminalRestore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPTYHelper$")
	cmd.Env = append(os.Environ(), "ARKEY_PTY_HELPER=1", "TERM=xterm-256color")
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	var output bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, terminal)
		close(readDone)
	}()
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 80; i++ {
		_, _ = terminal.Write([]byte("jk"))
		cols := uint16(60 + i%41)
		_ = pty.Setsize(terminal, &pty.Winsize{Rows: uint16(18 + i%12), Cols: cols})
	}
	_, _ = terminal.Write([]byte("q"))
	if err := cmd.Wait(); err != nil {
		t.Fatalf("TUI process: %v", err)
	}
	_ = terminal.Close()
	<-readDone
	rendered := output.String()
	enter := strings.Index(rendered, "\x1b[?1049h")
	exit := strings.LastIndex(rendered, "\x1b[?1049l")
	if enter < 0 || exit < enter {
		t.Fatalf("alternate screen was not cleanly entered/restored; bytes=%d", len(rendered))
	}
}
