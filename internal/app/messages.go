package app

type statusTickMsg struct{}
type resizeCommitMsg struct {
	generation    uint64
	width, height int
}

type statusRefreshedMsg struct {
	generation uint64
	status     Status
	err        error
}
type modelsDiscoveredMsg struct {
	generation uint64
	models     []ModelSummary
	refresh    bool
	err        error
}
type frontierSelectedMsg struct {
	generation uint64
	status     Status
	err        error
}
type localActivatedMsg struct {
	generation uint64
	status     Status
	err        error
}
type localUnloadedMsg struct {
	generation uint64
	status     Status
	err        error
}
type gpuScannedMsg struct {
	generation uint64
	status     Status
	err        error
}
