package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/JulianAbeleda/arkey_v3/internal/ui"
)

type screen uint8

const (
	mainScreen screen = iota
	tuiScreen
	configScreen
	localScreen
	modelsScreen
	frontierScreen
)

// Model is the complete display state. Effects enter only through Services.
type Model struct {
	services                    Services
	screen                      screen
	stack                       []screen
	cursors                     map[screen]int
	width, height               int
	dark                        bool
	status                      Status
	models                      []ModelSummary
	busy                        bool
	notice, errText             string
	launch                      *LaunchPlan
	spinner                     spinner.Model
	generation                  uint64
	opCancel                    context.CancelFunc
	rootContext                 context.Context
	resizeGeneration            uint64
	pendingWidth, pendingHeight int
}

func New(services Services) Model {
	return NewWithContext(services, context.Background())
}

func NewWithContext(services Services, root context.Context) Model {
	if services == nil {
		services = NopServices{}
	}
	if root == nil {
		root = context.Background()
	}
	return Model{services: services, cursors: map[screen]int{}, dark: true, spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)), rootContext: root}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.refresh(0), statusTick())
}

func statusTick() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return statusTickMsg{} })
}
func (m Model) refresh(generation uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.rootContext, 5*time.Second)
		defer cancel()
		s, err := m.services.Refresh(ctx)
		return statusRefreshedMsg{generation: generation, status: s, err: err}
	}
}
func (m Model) discover(parent context.Context, generation uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		v, err := m.services.DiscoverModels(ctx)
		return modelsDiscoveredMsg{generation: generation, models: v, err: err}
	}
}
func (m Model) selectFrontier(parent context.Context, generation uint64, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		s, err := m.services.SelectFrontier(ctx, name)
		return frontierSelectedMsg{generation: generation, status: s, err: err}
	}
}
func (m Model) activateLocal(parent context.Context, generation uint64, v ModelSummary) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 6*time.Minute)
		defer cancel()
		s, err := m.services.ActivateLocal(ctx, "llama", v)
		return localActivatedMsg{generation: generation, status: s, err: err}
	}
}
func (m Model) unloadLocal(parent context.Context, generation uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		s, err := m.services.UnloadLocal(ctx)
		return localUnloadedMsg{generation: generation, status: s, err: err}
	}
}
func (m Model) scanGPU(parent context.Context, generation uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		s, err := m.services.ScanGPU(ctx)
		return gpuScannedMsg{generation: generation, status: s, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch x := msg.(type) {
	case statusTickMsg:
		cmds = append(cmds, statusTick())
		if !m.busy {
			cmds = append(cmds, m.refresh(m.generation))
		}
	case tea.WindowSizeMsg:
		if m.width == 0 || m.height == 0 {
			m.width, m.height = x.Width, x.Height
			break
		}
		m.pendingWidth, m.pendingHeight = x.Width, x.Height
		m.resizeGeneration++
		generation := m.resizeGeneration
		width, height := x.Width, x.Height
		cmds = append(cmds, tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
			return resizeCommitMsg{generation: generation, width: width, height: height}
		}))
	case resizeCommitMsg:
		if x.generation == m.resizeGeneration {
			m.width, m.height = x.width, x.height
		}
	case tea.BackgroundColorMsg:
		m.dark = x.IsDark()
	case spinner.TickMsg:
		if m.busy {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(x)
			cmds = append(cmds, cmd)
		}
	case statusRefreshedMsg:
		if x.generation != m.generation {
			break
		}
		if x.err != nil {
			m.errText = x.err.Error()
		} else {
			m.status = x.status
		}
	case modelsDiscoveredMsg:
		if x.generation != m.generation {
			break
		}
		m.finishOperation()
		if x.err != nil {
			m.errText = x.err.Error()
		} else {
			m.models = x.models
			m.push(modelsScreen)
		}
	case frontierSelectedMsg:
		if x.generation != m.generation {
			break
		}
		m.finishOperation()
		if x.err != nil {
			m.errText = x.err.Error()
		} else {
			m.status = x.status
			m.notice = "Frontier route selected."
		}
	case localActivatedMsg:
		if x.generation != m.generation {
			break
		}
		m.finishOperation()
		if x.err != nil {
			m.errText = x.err.Error()
		} else {
			m.status = x.status
			m.notice = "Local route is ready and selected."
		}
	case localUnloadedMsg:
		if x.generation != m.generation {
			break
		}
		m.finishOperation()
		if x.err != nil {
			m.errText = x.err.Error()
		} else {
			m.status = x.status
			m.notice = "Local model unloaded. Saved model selection is unchanged."
		}
	case gpuScannedMsg:
		if x.generation != m.generation {
			break
		}
		m.finishOperation()
		if x.err != nil {
			m.errText = x.err.Error()
		} else {
			m.status = x.status
			m.notice = "GPU scan complete."
		}
	case tea.KeyPressMsg:
		key := x.String()
		if key == "ctrl+c" || key == "q" {
			if m.busy {
				m.cancelOperation()
				m.notice = "Operation canceled."
				return m, tea.Batch(cmds...)
			}
			return m, tea.Quit
		}
		if m.busy {
			if key == "left" || key == "esc" || key == "backspace" || key == "b" {
				m.cancelOperation()
				m.back()
			}
			return m, tea.Batch(cmds...)
		}
		switch key {
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "left", "esc", "backspace", "b":
			m.back()
		case "d":
			if m.screen == modelsScreen {
				cmds = append(cmds, m.unloadSelected())
			}
		case "right", "enter", "1", "2", "3", "4", "5", "6", "7", "8", "9":
			if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
				m.cursors[m.screen] = int(key[0] - '1')
			}
			cmds = append(cmds, m.activate())
		}
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) unloadSelected() tea.Cmd {
	if !m.status.LocalActive || len(m.models) == 0 {
		m.notice = "No local model process is active."
		return nil
	}
	cursor := m.cursors[modelsScreen]
	if cursor < 0 || cursor >= len(m.models) || !sameModel(m.models[cursor].Path, m.status.LoadedModel) {
		m.notice = "Move to the loaded model before unloading it."
		return nil
	}
	generation, ctx := m.begin()
	return tea.Batch(m.spinner.Tick, m.unloadLocal(ctx, generation))
}

func sameModel(left, right string) bool {
	return left != "" && right != "" && filepath.Clean(left) == filepath.Clean(right)
}

func (m *Model) push(next screen) {
	m.stack = append(m.stack, m.screen)
	m.screen = next
	m.errText = ""
	m.notice = ""
}
func (m *Model) begin() (uint64, context.Context) {
	if m.opCancel != nil {
		m.opCancel()
	}
	m.generation++
	m.busy = true
	m.errText = ""
	m.notice = ""
	ctx, cancel := context.WithCancel(m.rootContext)
	m.opCancel = cancel
	return m.generation, ctx
}
func (m *Model) cancelOperation() {
	if m.opCancel != nil {
		m.opCancel()
	}
	m.opCancel = nil
	m.generation++
	m.busy = false
}
func (m *Model) finishOperation() {
	if m.opCancel != nil {
		m.opCancel()
	}
	m.opCancel = nil
	m.busy = false
}
func (m *Model) back() {
	if len(m.stack) == 0 {
		return
	}
	m.screen = m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	m.errText = ""
	m.notice = ""
}
func (m *Model) move(delta int) {
	n := len(m.items())
	if n == 0 {
		return
	}
	v := (m.cursors[m.screen] + delta) % n
	if v < 0 {
		v += n
	}
	m.cursors[m.screen] = v
}
func (m *Model) activate() tea.Cmd {
	items := m.items()
	if len(items) == 0 {
		return nil
	}
	c := m.cursors[m.screen]
	if c < 0 || c >= len(items) {
		c = 0
		m.cursors[m.screen] = 0
	}
	item := items[c]
	switch m.screen {
	case mainScreen:
		if c == 0 {
			m.push(tuiScreen)
		} else if c == 1 {
			m.push(configScreen)
		} else {
			return tea.Quit
		}
	case tuiScreen:
		selected := m.selectedModel()
		if selected == "" || selected == "arkey-local-" {
			m.errText = "No usable AI route is selected. Open Config first."
			return nil
		}
		m.launch = &LaunchPlan{Model: selected}
		return tea.Quit
	case configScreen:
		if c == 0 {
			m.push(localScreen)
		} else if c == 1 {
			m.push(frontierScreen)
		} else {
			generation, ctx := m.begin()
			return tea.Batch(m.spinner.Tick, m.scanGPU(ctx, generation))
		}
	case localScreen:
		if c == 0 {
			m.notice = "tinygrad is in development and unavailable."
		} else {
			generation, ctx := m.begin()
			return tea.Batch(m.spinner.Tick, m.discover(ctx, generation))
		}
	case modelsScreen:
		generation, ctx := m.begin()
		return tea.Batch(m.spinner.Tick, m.activateLocal(ctx, generation, m.models[c]))
	case frontierScreen:
		generation, ctx := m.begin()
		return tea.Batch(m.spinner.Tick, m.selectFrontier(ctx, generation, strings.ToLower(item.Label)))
	}
	return nil
}

func (m Model) selectedModel() string {
	if m.status.Route.Mode == "local" {
		return "arkey-local-" + m.status.Route.LocalRuntime
	}
	return m.status.Route.Model
}
func (m Model) items() []ui.Item {
	switch m.screen {
	case mainScreen:
		return []ui.Item{
			{Key: "1", Label: "TUI", Detail: "choose the modded client", State: "Arkey Codex"},
			{Key: "2", Label: "Config", Detail: "routes and hardware", State: m.status.Route.Model},
			{Key: "3", Label: "Exit", Detail: "close Arkey", State: "quit"},
		}
	case tuiScreen:
		return []ui.Item{{Key: "1", Label: "Arkey Codex (modded)", Detail: "custom client, not official Codex", State: m.selectedModel()}}
	case configScreen:
		return []ui.Item{
			{Key: "1", Label: "Local", Detail: "runtime → installed model", State: m.localRuntimeState()},
			{Key: "2", Label: "Frontier", Detail: "hosted AI provider", State: m.status.Route.Backend},
			{Key: "3", Label: "GPU Auto-scan", Detail: "detect and align llama.cpp", State: m.status.GPU},
		}
	case localScreen:
		return []ui.Item{
			{Key: "1", Label: "tinygrad", Detail: "coming later · unavailable", State: "development", Disabled: true},
			{Key: "2", Label: "llama.cpp", Detail: "local GGUF server", State: m.localRuntimeState()},
		}
	case modelsScreen:
		out := make([]ui.Item, len(m.models))
		for i, v := range m.models {
			state := "installed"
			if sameModel(v.Path, m.status.Route.LocalModel) {
				state = "selected"
			}
			if m.status.LocalActive && sameModel(v.Path, m.status.LoadedModel) {
				state = "◐ starting"
				if m.status.LocalLoaded {
					state = "● loaded"
				}
			}
			out[i] = ui.Item{Key: fmt.Sprint(i + 1), Label: v.Name, Detail: v.Detail, State: state}
		}
		return out
	case frontierScreen:
		return []ui.Item{
			{Key: "1", Label: "DeepSeek", Detail: "hosted via MoonBridge"},
			{Key: "2", Label: "Codex", Detail: "hosted via MoonBridge"},
			{Key: "3", Label: "Claude", Detail: "hosted via MoonBridge"},
		}
	}
	return nil
}
func (m Model) localRuntimeState() string {
	if m.status.LocalLoaded {
		return "● loaded"
	}
	if m.status.LocalActive {
		return "◐ starting"
	}
	if m.status.Route.LocalModel != "" {
		return "stopped"
	}
	return "not selected"
}
func (m Model) title() string {
	return []string{"BOOT", "TUI", "CONFIG", "CONFIG · LOCAL", "LOCAL · LLAMA · MODELS", "CONFIG · FRONTIER"}[m.screen]
}
func (m Model) subtitle() string {
	switch m.screen {
	case mainScreen:
		return "Workspace: " + m.status.Workspace + " · Runtime: " + m.status.Runtime + " · MoonBridge: " + m.status.MoonBridge
	case tuiScreen:
		return "Arkey-modified clients; Arkey Codex is not official Codex."
	case modelsScreen:
		return "Select a GGUF to load. The active model is marked ● loaded."
	}
	return "Configure AI routes and local hardware alignment."
}
func (m Model) View() tea.View {
	busyLabel := ""
	if m.busy {
		if m.status.ReducedMotion {
			busyLabel = "Working…"
		} else {
			busyLabel = m.spinner.View() + " Working…"
		}
	}
	content := ui.Render(ui.Screen{Title: m.title(), Subtitle: m.subtitle(), Items: m.items(), Cursor: m.cursors[m.screen], Width: m.width, Height: m.height, Dark: m.dark, BusyLabel: busyLabel, Notice: m.notice, Error: m.errText, Help: m.help()})
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "Arkey"
	return v
}
func (m Model) help() string {
	if m.screen == modelsScreen {
		return "↑/↓ or j/k move · Enter/→ load · d unload · ←/Esc/b back · q quit"
	}
	return "↑/↓ or j/k move · Enter/→ select · ←/Esc/b back · q quit"
}

// LaunchPlan returns a copy of the request after Program.Run exits.
func (m Model) LaunchPlan() *LaunchPlan {
	if m.launch == nil {
		return nil
	}
	v := *m.launch
	return &v
}
func (m Model) ScreenName() string { return m.title() }
func (m Model) Cursor() int        { return m.cursors[m.screen] }

var _ tea.Model = Model{}
