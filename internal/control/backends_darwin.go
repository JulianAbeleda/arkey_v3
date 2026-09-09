//go:build darwin

package control

import (
	"github.com/JulianAbeleda/arkey_v3/internal/gpu"
	arkeyruntime "github.com/JulianAbeleda/arkey_v3/internal/runtime"
)

func gpuInspector(r gpu.Runner) gpu.Inspector { return gpu.DeviceInspector{Runner: r} }

// The runtime backends on macOS read process identity through ps/lsof and always
// run the llama/MoonBridge processes in direct mode: macOS has no systemd, so
// DirectOnlyService reports Available()==false and the controller falls back to
// DirectLauncher for start and its process-group Terminate for stop.

func systemInspector(r arkeyruntime.CommandRunner) arkeyruntime.Inspector {
	return arkeyruntime.DarwinInspector{Runner: r}
}

func serviceManager(_ arkeyruntime.CommandRunner) arkeyruntime.Service {
	return arkeyruntime.DirectOnlyService{}
}

func libraryBackend(r arkeyruntime.CommandRunner) arkeyruntime.Backend {
	return arkeyruntime.OtoolBackend{Runner: r}
}
