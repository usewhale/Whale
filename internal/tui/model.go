package tui

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/app/service"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/tui/composer"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

type mode int

const (
	modeChat mode = iota
	modeApproval
	modeSessionPicker
	modeUserInput
	modeModelPicker
	modePermissionsPicker
	modePlanImplementation
)

type page int

const (
	pageChat page = iota
	pageLogs
	pageDiff
)

type model struct {
	svc        *service.Service
	dispatch   func(service.Intent)
	input      composer.Composer
	viewport   viewport.Model
	assembler  *tuirender.Assembler
	transcript []tuirender.UIMessage
	logs       []logEntry
	diffs      []diffEntry
	width      int
	height     int
	mode       mode
	page       page
	status     string
	busy       bool
	busySince  time.Time
	stopping   bool
	sidebar    bool
	model      string
	effort     string
	thinking   string
	chatMode   string
	product    string
	version    string
	cwd        string
	approval   struct {
		toolCallID string
		toolName   string
		reason     string
		metadata   map[string]any
		selected   int
	}
	sessionChoices []string
	sessionIndex   int
	userInput      struct {
		toolCallID     string
		toolName       string
		questions      []core.UserInputQuestion
		index          int
		selectedOption int
		answers        []core.UserInputAnswer
	}
	palette struct {
		actions  []paletteAction
		selected int
	}
	logFilterInput textinput.Model
	logFilter      string
	slash          struct {
		all      []string
		autoRun  map[string]bool
		matches  []string
		selected int
	}
	modelPicker struct {
		stage     int // 0 model, 1 effort, 2 thinking
		models    []string
		efforts   []string
		thinkings []string
		modelIx   int
		effIx     int
		thinkIx   int
	}
	permissionsPicker struct {
		choices []string
		index   int
	}
	planImplementation struct {
		index int
	}
	sawPlanThisTurn                bool
	sawAssistantThisTurn           bool
	sawReasoningThisTurn           bool
	sawTerminalToolOutcomeThisTurn bool
	quitArmedUntil                 time.Time
	promptHistory                  []string
	historyIndex                   int
	historyDraft                   string
	lastHistoryText                string
	inHistoryNav                   bool
	queuedPrompts                  []queuedPrompt
	nativeScrollbackPrinted        int
}

type queuedPrompt struct {
	Text string
}

type paletteAction struct {
	Label string
	Run   func(*model)
}

type logEntry struct {
	Kind    string
	Source  string
	Summary string
	Raw     string
}

type diffEntry struct {
	Source string
	Line   string
}

type svcMsg service.Event

type errMsg struct{ err error }
type quitTimeoutMsg struct{}
type busyTickMsg struct{}

func newModel(svc *service.Service, modelName, effort, thinking string) model {
	filter := textinput.New()
	filter.Placeholder = "filter logs (press /)"
	filter.Prompt = "/"
	filter.CharLimit = 200
	vp := viewport.New(80, 20)
	if modelName == "" {
		modelName = defaults.DefaultModel
	}
	if effort == "" {
		effort = defaults.DefaultReasoningEffort
	}
	if thinking == "" {
		thinking = "on"
	}
	m := model{
		svc:            svc,
		input:          composer.New(),
		viewport:       vp,
		assembler:      tuirender.NewAssembler(),
		status:         "ready",
		page:           pageChat,
		sidebar:        false,
		logFilterInput: filter,
		model:          modelName,
		effort:         effort,
		thinking:       thinking,
		chatMode:       "agent",
		product:        "Whale",
		version:        resolveVersion(),
		cwd:            resolveWorkingDirectory(),
		historyIndex:   -1,
	}
	if svc != nil {
		m.dispatch = svc.Dispatch
	}
	m.slash.all = parseSlashCommands(app.CommandsHelp)
	m.slash.autoRun = buildSlashAutoRunMap(app.CommandsHelp)
	m.resetTranscriptWithHeader()
	return m
}

func (m *model) dispatchIntent(in service.Intent) {
	if m.dispatch != nil {
		m.dispatch(in)
	}
}

func waitEventCmd(svc *service.Service) tea.Cmd {
	return func() tea.Msg {
		ev := <-svc.Events()
		return svcMsg(ev)
	}
}

func armQuitCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return quitTimeoutMsg{} })
}

func busyTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return busyTickMsg{} })
}

// clearScreenCmd clears the visible terminal and scrollback buffer,
// then forces a full TUI redraw. Uses ANSI \033[3J to clear scrollback
// in addition to \033[H\033[2J (visible area).
func clearScreenCmd() tea.Cmd {
	return tea.ClearScreen
}

func (m model) Init() tea.Cmd { return waitEventCmd(m.svc) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(max(20, m.width-4))
		mainWidth, bodyHeight := m.layoutDims()
		m.viewport.Width = max(10, mainWidth-2)
		m.viewport.Height = bodyHeight - 2
		m.refreshViewportContent()
		return m, nil
	case svcMsg:
		eventCmd, quit, direct := m.handleServiceEvent(service.Event(msg))
		if quit {
			return m, tea.Quit
		}
		if direct {
			return m, eventCmd
		}
		return m, tea.Sequence(eventCmd, m.flushNativeScrollbackCmd(), waitEventCmd(m.svc))
	case quitTimeoutMsg:
		if !m.quitArmedUntil.IsZero() && time.Now().After(m.quitArmedUntil) {
			m.quitArmedUntil = time.Time{}
			if m.status == "Press Ctrl+C again to quit" {
				m.status = "ready"
			}
		}
		return m, nil
	case busyTickMsg:
		if m.busy {
			return m, busyTickCmd()
		}
		return m, nil
	case tea.MouseMsg:
		m.refreshViewportContent()
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.viewport.LineUp(3)
		case tea.MouseButtonWheelDown:
			m.viewport.LineDown(3)
		}
		return m, nil
	case tea.KeyMsg:
		cmd, quit, handled := m.handleKeyMsg(msg)
		if quit {
			return m, tea.Quit
		}
		if handled {
			return m, cmd
		}
	}
	prevInput := m.input.Value()
	cmd := m.input.Update(msg)
	m.updateSlashMatches()
	if m.inHistoryNav && m.input.Value() != prevInput {
		m.resetHistoryNavigation()
	}
	m.refreshViewportContent()
	return m, cmd
}
