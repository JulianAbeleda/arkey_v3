package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/JulianAbeleda/arkey_v3/internal/app"
	"github.com/JulianAbeleda/arkey_v3/internal/cli"
	"github.com/JulianAbeleda/arkey_v3/internal/codex"
	"github.com/JulianAbeleda/arkey_v3/internal/control"
	"golang.org/x/sys/unix"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version") {
		fmt.Printf("arkey %s (%s)\n", version, commit)
		return 0
	}
	parsed, err := cli.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Arkey:", err)
		if errors.Is(err, cli.ErrUsage) {
			return 2
		}
		return 1
	}
	home := os.Getenv("ARKEY_USER_HOME")
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Arkey: determine home:", err)
			return 1
		}
	}
	workspace, err := os.Getwd()
	if err != nil {
		workspace = home
	}
	services, err := control.New(home, workspace)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Arkey configuration:", err)
		return 1
	}
	model := services.SelectedModel()
	if shouldBoot(parsed) {
		uiCtx, cancelUI := context.WithCancel(context.Background())
		program := tea.NewProgram(app.NewWithContext(services, uiCtx), tea.WithContext(uiCtx))
		finalModel, runErr := program.Run()
		cancelUI()
		if runErr != nil {
			fmt.Fprintln(os.Stderr, "Arkey TUI:", runErr)
			return 1
		}
		finished, ok := finalModel.(app.Model)
		if !ok {
			fmt.Fprintln(os.Stderr, "Arkey TUI returned an unexpected model")
			return 1
		}
		launch := finished.LaunchPlan()
		if launch == nil {
			return 0
		}
		model = launch.Model
	}
	if parsed.HasModel {
		model = parsed.ModelOverride
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = codex.DefaultHome(home)
	}
	plan, err := codex.Build(codex.BuildOptions{
		Parsed: parsed, Model: model, Binary: services.CodexBinary,
		CodexHome: codexHome, Environment: os.Environ(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Arkey Codex plan:", err)
		return 1
	}
	if err = plan.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	if err = services.PrepareLaunch(ctx, model); err != nil {
		fmt.Fprintln(os.Stderr, "Arkey route:", err)
		return 1
	}
	binary, err := filepath.Abs(plan.Binary)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Arkey Codex path:", err)
		return 1
	}
	if err = unix.Exec(binary, append([]string{binary}, plan.Args...), plan.Env); err != nil {
		fmt.Fprintln(os.Stderr, "Launch Arkey Codex:", err)
		return 1
	}
	return 0
}

func shouldBoot(parsed cli.Options) bool {
	if parsed.ForceBoot {
		return true
	}
	if parsed.SuppressBoot || len(parsed.CodexArgs) != 0 || os.Getenv("ARKEY_BOOT") == "0" {
		return false
	}
	return isTerminal(os.Stdin) && isTerminal(os.Stdout)
}

func isTerminal(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	return err == nil
}
