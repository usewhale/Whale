package tui

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/app"
	appcommands "github.com/usewhale/whale/internal/app/commands"
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
	modePermissionsProjectTrustConfirm
	modePermissionsProjectClearConfirm
	modePlanImplementation
	modeSkillsMenu
	modeSkillsManager
	modePluginsManager
	modeReviewMenu
	modeReviewBranchPicker
	modeReviewCommitPicker
	modeReviewPRPicker
	modeHelp
)

type page int

const (
	pageChat page = iota
	pageLogs
	pageDiff
)

type model struct {
	svc                  *service.Service
	dispatch             func(service.Intent)
	input                composer.Composer
	viewport             viewport.Model
	chat                 chatList
	assembler            *tuirender.Assembler
	pendingToolCalls     map[string]struct{}
	transcript           []tuirender.UIMessage
	sessionID            string
	startupHeaderPrinted bool
	startupHeaderOnce    *bool
	ephemeralMessages    []tuirender.UIMessage
	logs                 []logEntry
	diffs                []diffEntry
	width                int
	height               int
	followTail           bool
	viewportFrozen       bool
	frozenChatMessages   []tuirender.UIMessage
	viewportLayoutReady  bool
	viewportLayoutPage   page
	viewportLayoutWidth  int
	viewportLayoutHeight int
	mode                 mode
	page                 page
	status               string
	busy                 bool
	busySince            time.Time
	providerRetryStatus  string
	providerRetryUntil   time.Time
	localSubmitPending   int
	localSubmitCommands  []string
	btwPanel             btwPanelState
	deferredPlanPicker   bool
	stopping             bool
	sidebar              bool
	model                string
	effort               string
	thinking             string
	viewMode             string
	chatMode             string
	product              string
	version              string
	cwd                  string
	cwdPath              string
	gitBranch            string
	approval             struct {
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
		all          []appcommands.SlashCommandSpec
		matches      []slashSuggestion
		selected     int
		argumentHint string
	}
	skills struct {
		all      []skillSuggestion
		matches  []skillSuggestion
		selected int
	}
	skillBinding *app.SkillBinding
	skillsMenu   struct {
		selected int
	}
	skillsManager struct {
		all      []skillManagerItem
		matches  []int
		selected int
		query    string
	}
	pluginsManager struct {
		all      []pluginManagerItem
		matches  []int
		selected int
	}
	reviewMenu struct {
		selected int
	}
	reviewTargetPicker reviewTargetPickerState
	help               struct {
		selected int
		offset   int
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
	permissionsProjectConfirm struct {
		index int
	}
	planImplementation struct {
		index int
	}
	lastProposedPlan               string
	sawPlanThisTurn                bool
	sawAssistantThisTurn           bool
	sawReasoningThisTurn           bool
	sawTerminalToolOutcomeThisTurn bool
	visibleAssistantThisTurn       string
	turnTranscriptStart            int
	quitArmedUntil                 time.Time
	promptHistory                  []string
	historyIndex                   int
	historyDraft                   string
	lastHistoryText                string
	inHistoryNav                   bool
	queuedPrompts                  []queuedPrompt
	nativeScrollbackPrinted        int
	pendingMouseCSIFragment        bool
	windowsPaste                   windowsPasteFallbackState
}

type queuedPrompt struct {
	Text         string
	SkillBinding *app.SkillBinding
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

type skillSuggestion struct {
	Name          string
	Description   string
	When          string
	SkillFilePath string
	Status        string
	Reason        string
}

type skillManagerItem struct {
	Name                string
	Description         string
	OriginalDescription string
	Status              string
	Reason              string
	Source              string
	Enabled             bool
	Toggleable          bool
}

type svcMsg service.Event
type svcBatchMsg []service.Event

type errMsg struct{ err error }
type quitTimeoutMsg struct{}
type busyTickMsg struct{}

const serviceDeltaFrame = 100 * time.Millisecond

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
	viewMode := app.ViewModeDefault
	if svc != nil {
		viewMode = svc.ViewMode()
	}
	m := model{
		svc:               svc,
		input:             composer.New(),
		viewport:          vp,
		chat:              newChatList(),
		assembler:         tuirender.NewAssembler(),
		pendingToolCalls:  map[string]struct{}{},
		startupHeaderOnce: new(bool),
		status:            "ready",
		followTail:        true,
		page:              pageChat,
		sidebar:           false,
		logFilterInput:    filter,
		width:             80,
		height:            24,
		model:             modelName,
		effort:            effort,
		thinking:          thinking,
		viewMode:          viewMode,
		chatMode:          "agent",
		product:           "Whale",
		version:           resolveVersion(),
		cwd:               resolveWorkingDirectory(),
		cwdPath:           resolveWorkingDirectoryPath(),
		historyIndex:      -1,
	}
	if svc != nil {
		m.dispatch = svc.Dispatch
	}
	m.slash.all = appcommands.DefaultSlashCommands()
	m.resetTranscript()
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
		if !shouldBatchServiceEvent(ev) {
			return svcMsg(ev)
		}
		events := appendBatchedServiceEvent(nil, ev)
		timer := time.NewTimer(serviceDeltaFrame)
		defer timer.Stop()
		for {
			select {
			case next := <-svc.Events():
				events = appendBatchedServiceEvent(events, next)
				if !shouldBatchServiceEvent(next) {
					return svcBatchMsg(events)
				}
			case <-timer.C:
				return svcBatchMsg(events)
			}
		}
	}
}

func appendBatchedServiceEvent(events []service.Event, ev service.Event) []service.Event {
	if shouldBatchServiceEvent(ev) && len(events) > 0 {
		last := &events[len(events)-1]
		if last.Kind == ev.Kind {
			last.Text += ev.Text
			return events
		}
	}
	return append(events, ev)
}

func shouldBatchServiceEvent(ev service.Event) bool {
	switch ev.Kind {
	case service.EventAssistantDelta, service.EventReasoningDelta, service.EventPlanDelta:
		return true
	default:
		return false
	}
}

func armQuitCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return quitTimeoutMsg{} })
}

func busyTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return busyTickMsg{} })
}

// clearScreenCmd clears the visible terminal, scrollback, and renderer cache.
func clearScreenCmd() tea.Cmd {
	return clearScreenCmdForOS(runtime.GOOS, os.Stdout)
}

func clearScreenCmdForOS(goos string, out io.Writer) tea.Cmd {
	if goos == "windows" {
		return func() tea.Msg {
			fmt.Fprint(out, "\033[H\033[2J\033[3J")
			return tea.ClearScreen()
		}
	}
	return func() tea.Msg {
		fmt.Fprint(out, "\033[H\033[2J\033[3J")
		return tea.ClearScreen()
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(waitEventCmd(m.svc), detectGitBranchCmd(m.cwdPath))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(max(20, m.width-4))
		headerCmd := m.startupHeaderPrintCmd()
		m.refreshViewportContent()
		return m, m.sequenceCmds(headerCmd)
	case svcMsg:
		eventCmd, quit, direct := m.handleServiceEvents([]service.Event{service.Event(msg)})
		if quit {
			return m, m.sequenceCmds(tea.Quit)
		}
		if direct {
			return m, m.sequenceCmds(eventCmd)
		}
		headerCmd := m.startupHeaderPrintCmd()
		scrollbackCmd := m.flushNativeScrollbackCmd()
		return m, m.sequenceCmds(eventCmd, headerCmd, scrollbackCmd, waitEventCmd(m.svc))
	case svcBatchMsg:
		eventCmd, quit, direct := m.handleServiceEvents([]service.Event(msg))
		if quit {
			return m, m.sequenceCmds(tea.Quit)
		}
		if direct {
			return m, m.sequenceCmds(eventCmd)
		}
		headerCmd := m.startupHeaderPrintCmd()
		scrollbackCmd := m.flushNativeScrollbackCmd()
		return m, m.sequenceCmds(eventCmd, headerCmd, scrollbackCmd, waitEventCmd(m.svc))
	case windowsDeferredEnterMsg:
		return m, m.sequenceCmds(m.handleWindowsDeferredEnter(msg))
	case windowsPendingEnterTailMsg:
		return m, m.sequenceCmds(m.handleWindowsPendingEnterTail(msg))
	case windowsPasteBurstFlushMsg:
		return m, m.sequenceCmds(m.handleWindowsPasteBurstFlush(msg))
	case quitTimeoutMsg:
		if !m.quitArmedUntil.IsZero() && time.Now().After(m.quitArmedUntil) {
			m.quitArmedUntil = time.Time{}
			if m.status == "Press Ctrl+C again to quit" {
				m.status = "ready"
			}
		}
		return m, m.sequenceCmds()
	case busyTickMsg:
		if m.busy {
			return m, m.sequenceCmds(busyTickCmd())
		}
		return m, m.sequenceCmds()
	case gitBranchUpdatedMsg:
		if msg.cwd == m.cwdPath {
			m.gitBranch = msg.branch
		}
		return m, m.sequenceCmds()
	case reviewCommitsLoadedMsg:
		m.handleReviewCommitsLoaded(msg)
		return m, m.sequenceCmds()
	case reviewBranchesLoadedMsg:
		m.handleReviewBranchesLoaded(msg)
		return m, m.sequenceCmds()
	case reviewPRsLoadedMsg:
		m.handleReviewPRsLoaded(msg)
		return m, m.sequenceCmds()
	case tea.KeyMsg:
		prevMainWidth, _ := m.layoutDims()
		prevBodyHeight := m.viewportBodyHeight(prevMainWidth)
		if !msg.Paste && m.consumeMouseCSIFragment(msg) {
			m.refreshViewportContent()
			return m, m.sequenceCmds()
		}
		cmd, quit, handled := m.handleKeyMsg(msg)
		if quit {
			return m, m.sequenceCmds(tea.Quit)
		}
		if handled {
			m.refreshViewportContentIfBodyHeightChanged(prevMainWidth, prevBodyHeight)
			return m, m.sequenceCmds(cmd)
		}
	}
	prevMainWidth, _ := m.layoutDims()
	prevBodyHeight := m.viewportBodyHeight(prevMainWidth)
	prevInput := m.input.Value()
	cmd := m.input.Update(msg)
	inputChanged := m.input.Value() != prevInput
	if inputChanged {
		m.resetWindowsPasteFallbackIfInputEmpty()
	}
	m.updateSlashMatches()
	if m.inHistoryNav && inputChanged {
		m.resetHistoryNavigation()
	}
	m.refreshViewportContentIfBodyHeightChanged(prevMainWidth, prevBodyHeight)
	return m, m.sequenceCmds(cmd)
}

func (m *model) sequenceCmds(cmds ...tea.Cmd) tea.Cmd {
	out := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			out = append(out, cmd)
		}
	}
	return tea.Sequence(out...)
}
