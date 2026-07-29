// Package app contains Arkey's UI state machine. It deliberately owns no I/O.
package app

import "context"

// Route is the persisted AI routing selection shown by the boot UI.
type Route struct {
	Mode, Backend, Model, LocalRuntime, LocalModel string
}

// Status is a cached, display-safe health snapshot supplied by the integration layer.
type Status struct {
	Workspace, Runtime, MoonBridge, GPU string
	Route                               Route
	LoadedModel                         string
	LocalActive                         bool
	LocalLoaded                         bool
	ReducedMotion                       bool
}

// ModelSummary is deliberately metadata-only: paths are never opened by the UI.
type ModelSummary struct {
	Path, Name, Detail string
}

// LaunchPlan is consumed by cmd/arkey only after Program.Run restores the terminal.
type LaunchPlan struct{ Model string }

// Services are narrow asynchronous boundaries. Implementations live in backend packages;
// every method must honor ctx and return promptly when cancelled.
type Services interface {
	Refresh(context.Context) (Status, error)
	DiscoverModels(context.Context) ([]ModelSummary, error)
	SelectFrontier(context.Context, string) (Status, error)
	ActivateLocal(context.Context, string, ModelSummary) (Status, error)
	UnloadLocal(context.Context) (Status, error)
	ScanGPU(context.Context) (Status, error)
}

// NopServices makes the model useful in previews and tests before backends are wired.
type NopServices struct{}

func (NopServices) Refresh(context.Context) (Status, error)                { return Status{}, nil }
func (NopServices) DiscoverModels(context.Context) ([]ModelSummary, error) { return nil, nil }
func (NopServices) SelectFrontier(context.Context, string) (Status, error) { return Status{}, nil }
func (NopServices) ActivateLocal(context.Context, string, ModelSummary) (Status, error) {
	return Status{}, nil
}
func (NopServices) UnloadLocal(context.Context) (Status, error) { return Status{}, nil }
func (NopServices) ScanGPU(context.Context) (Status, error)     { return Status{}, nil }
