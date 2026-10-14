//go:build linux

package control

import (
	"github.com/JulianAbeleda/arkey_v3/internal/gpu"
	arkeyruntime "github.com/JulianAbeleda/arkey_v3/internal/runtime"
)

func gpuInspector(r gpu.Runner) gpu.Inspector { return gpu.LDDInspector{Runner: r} }

// The runtime backends on Linux read process identity from procfs and manage the
// llama/MoonBridge processes through a transient systemd --user unit.

func systemInspector(_ arkeyruntime.CommandRunner) arkeyruntime.Inspector {
	return arkeyruntime.LinuxInspector{}
}

func serviceManager(r arkeyruntime.CommandRunner) arkeyruntime.Service {
	return arkeyruntime.SystemdService{Runner: r}
}

func libraryBackend(r arkeyruntime.CommandRunner) arkeyruntime.Backend {
	return arkeyruntime.CommandBackend{Runner: r}
}
