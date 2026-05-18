package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/s1liconcow/skiff/internal/client"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	focusServices = "services"
	focusStateful = "stateful"
	focusSagas    = "sagas"
	focusEvents   = "events"
)

type Model struct {
	client   client.Interface
	sagas    SagasClient
	service  string
	traceID  string
	fresh    bool
	readOnly bool
	noColor  bool
	width    int
	height   int
	now      func() time.Time
	keys     KeyMap
	help     help.Model
	spinner  spinner.Model

	dashboard Dashboard
	loading   bool
	err       error
	focus     string
	selected  int
	action    *Action
	quitting  bool
}

type KeyMap struct {
	Up       key.Binding
	Down     key.Binding
	NextPane key.Binding
	Refresh  key.Binding
	Doctor   key.Binding
	Logs     key.Binding
	Metrics  key.Binding
	Events   key.Binding
	Rollback key.Binding
	Approve  key.Binding
	Saga     key.Binding
	Help     key.Binding
	Quit     key.Binding
}

type loadedMsg struct {
	dashboard Dashboard
	err       error
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("j", "down")),
		NextPane: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "pane")),
		Refresh:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Doctor:   key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "doctor")),
		Logs:     key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs")),
		Metrics:  key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "metrics")),
		Events:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "events")),
		Rollback: key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "rollback")),
		Approve:  key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "approve")),
		Saga:     key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "saga")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.NextPane, k.Up, k.Down, k.Refresh, k.Doctor, k.Logs, k.Help, k.Quit}
}

func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.NextPane, k.Refresh, k.Help},
		{k.Doctor, k.Logs, k.Metrics, k.Events, k.Saga},
		{k.Rollback, k.Approve, k.Quit},
	}
}

func New(opts Options) Model {
	sagas := opts.Sagas
	if sagas == nil {
		if c, ok := opts.Client.(SagasClient); ok {
			sagas = c
		}
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	width := opts.Width
	if width <= 0 {
		width = 118
	}
	height := opts.Height
	if height <= 0 {
		height = 34
	}
	keys := DefaultKeyMap()
	return Model{
		client:   opts.Client,
		sagas:    sagas,
		service:  strings.TrimSpace(opts.Service),
		traceID:  opts.TraceID,
		fresh:    opts.Fresh,
		readOnly: opts.ReadOnly,
		noColor:  opts.NoColor,
		width:    width,
		height:   height,
		now:      now,
		keys:     keys,
		help:     newHelpModel(opts.NoColor, width),
		spinner:  newSpinner(opts.NoColor),
		loading:  true,
		focus:    focusServices,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.load, m.spinner.Tick)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = msg.Width
		return m, nil
	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case loadedMsg:
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.dashboard = msg.dashboard
			m.clampSelection()
		}
		return m, nil
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
			return m, tea.Quit
		case key.Matches(msg, m.keys.Refresh):
			m.loading = true
			m.action = nil
			return m, tea.Batch(m.load, m.spinner.Tick)
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			return m, nil
		case key.Matches(msg, m.keys.NextPane):
			m.nextFocus()
			m.action = nil
			m.clampSelection()
			return m, nil
		case key.Matches(msg, m.keys.Up):
			m.selected--
			m.clampSelection()
			return m, nil
		case key.Matches(msg, m.keys.Down):
			m.selected++
			m.clampSelection()
			return m, nil
		default:
			if action, ok := m.ActionForKey(msg.String()); ok {
				m.action = &action
				return m, nil
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	return render(m)
}

func (m Model) Dashboard() Dashboard {
	return m.dashboard
}

func (m Model) Load(ctx context.Context) (Model, error) {
	dashboard, err := m.fetch(ctx)
	if err != nil {
		m.err = err
		m.loading = false
		return m, err
	}
	m.dashboard = dashboard
	m.err = nil
	m.loading = false
	m.clampSelection()
	return m, nil
}

func (m Model) ActionForKey(value string) (Action, bool) {
	service := m.selectedService()
	stateful := m.selectedStatefulGroup()
	saga := m.selectedSaga()
	switch value {
	case "d":
		if m.focus == focusStateful && stateful.Group != "" {
			return m.readAction("stateful_doctor", "Doctor", "d", fmt.Sprintf("skiff stateful doctor %s --fresh --format json --trace-id %s", stateful.Group, m.trace()), "Run doctor diagnostics for the selected StatefulGroup"), true
		}
		if service.Service == "" {
			return Action{}, false
		}
		return m.readAction("doctor", "Doctor", "d", fmt.Sprintf("skiff doctor %s --fresh --format json --trace-id %s", service.Service, m.trace()), "Run doctor diagnostics for the selected service"), true
	case "l":
		if m.focus == focusStateful && stateful.Group != "" {
			return m.readAction("stateful_logs", "Logs", "l", fmt.Sprintf("skiff stateful logs %s --since 20m --format json --trace-id %s", stateful.Group, m.trace()), "Open recent StatefulGroup logs"), true
		}
		if service.Service == "" {
			return Action{}, false
		}
		return m.readAction("logs", "Logs", "l", fmt.Sprintf("skiff logs %s --since 20m --format json --trace-id %s", service.Service, m.trace()), "Open recent service logs"), true
	case "m":
		if m.focus == focusStateful && stateful.Group != "" {
			return m.readAction("stateful_metrics", "Metrics", "m", fmt.Sprintf("skiff stateful metrics %s --from -30m --format json --trace-id %s", stateful.Group, m.trace()), "Inspect StatefulGroup metrics"), true
		}
		if service.Service == "" {
			return Action{}, false
		}
		return m.readAction("metrics", "Metrics", "m", fmt.Sprintf("skiff metrics %s --from -30m --format json --trace-id %s", service.Service, m.trace()), "Inspect service metrics"), true
	case "e":
		if m.focus == focusStateful && stateful.Group != "" {
			return m.readAction("stateful_events", "Events", "e", fmt.Sprintf("skiff ops events %s --format json --trace-id %s", stateful.Group, m.trace()), "List recent StatefulGroup events"), true
		}
		if service.Service == "" {
			return Action{}, false
		}
		return m.readAction("events", "Events", "e", fmt.Sprintf("skiff ops events %s --format json --trace-id %s", service.Service, m.trace()), "List recent service events"), true
	case "x":
		if saga.SagaID == "" {
			return Action{}, false
		}
		return m.readAction("saga_inspect", "Saga", "x", fmt.Sprintf("skiff ops inspect %s --format json --trace-id %s", saga.SagaID, m.trace()), "Inspect selected saga graph"), true
	case "b":
		if service.Service == "" {
			return Action{}, false
		}
		return m.mutatingAction("rollback", "Rollback", "b", fmt.Sprintf("skiff rollback %s --to previous-stable --format json --trace-id %s", service.Service, m.trace()), "Start previous-stable rollback for selected service", schema.RiskMedium, schema.Reversible), true
	case "a":
		if saga.SagaID == "" {
			return Action{}, false
		}
		step := firstCurrentStep(saga)
		if step == "" {
			step = "<step>"
		}
		return m.mutatingAction("approve", "Approve", "a", fmt.Sprintf("skiff ops approve %s --step %s --format json --trace-id %s", saga.SagaID, step, m.trace()), "Approve selected waiting saga step", schema.RiskHigh, schema.Compensatable), true
	default:
		return Action{}, false
	}
}

func (m Model) load() tea.Msg {
	dashboard, err := m.fetch(context.Background())
	return loadedMsg{dashboard: dashboard, err: err}
}

func (m Model) fetch(ctx context.Context) (Dashboard, error) {
	if m.client == nil {
		return Dashboard{}, fmt.Errorf("tui client is required")
	}
	status, err := m.client.Status(ctx, client.StatusOptions{Service: m.service, Fresh: m.fresh, TraceID: m.traceID})
	if err != nil {
		return Dashboard{}, err
	}
	events, err := m.client.Events(ctx, client.EventOptions{Scope: "recent", Limit: 12, Fresh: m.fresh, TraceID: m.traceID})
	if err != nil {
		return Dashboard{}, err
	}
	var sagas []client.SagaSummary
	if m.sagas != nil {
		list, err := m.sagas.Sagas(ctx, client.SagaOptions{Fresh: m.fresh, TraceID: m.traceID})
		if err != nil {
			return Dashboard{}, err
		}
		sagas = append([]client.SagaSummary(nil), list.Sagas...)
	}
	return Dashboard{
		Status:    *status,
		Sagas:     sagas,
		Events:    append([]schema.Event(nil), events.Events...),
		Freshness: status.Freshness,
		Source:    status.Source,
	}, nil
}

func (m *Model) nextFocus() {
	switch m.focus {
	case focusServices:
		m.focus = focusStateful
	case focusStateful:
		m.focus = focusSagas
	case focusSagas:
		m.focus = focusEvents
	default:
		m.focus = focusServices
	}
	m.selected = 0
}

func (m *Model) clampSelection() {
	max := 0
	switch m.focus {
	case focusServices:
		max = len(m.dashboard.Status.Services)
	case focusStateful:
		max = len(m.dashboard.Status.StatefulGroups)
	case focusSagas:
		max = len(m.dashboard.Sagas)
	case focusEvents:
		max = len(m.dashboard.Events)
	}
	if max == 0 {
		m.selected = 0
		return
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= max {
		m.selected = max - 1
	}
}

func (m Model) selectedStatefulGroup() client.StatefulGroup {
	if len(m.dashboard.Status.StatefulGroups) == 0 {
		return client.StatefulGroup{}
	}
	if m.focus == focusStateful && m.selected < len(m.dashboard.Status.StatefulGroups) {
		return m.dashboard.Status.StatefulGroups[m.selected]
	}
	return m.dashboard.Status.StatefulGroups[0]
}

func (m Model) selectedService() client.ServiceStatus {
	if len(m.dashboard.Status.Services) == 0 {
		return client.ServiceStatus{}
	}
	if m.focus == focusServices && m.selected < len(m.dashboard.Status.Services) {
		return m.dashboard.Status.Services[m.selected]
	}
	return m.dashboard.Status.Services[0]
}

func (m Model) selectedSaga() client.SagaSummary {
	if len(m.dashboard.Sagas) == 0 {
		return client.SagaSummary{}
	}
	if m.focus == focusSagas && m.selected < len(m.dashboard.Sagas) {
		return m.dashboard.Sagas[m.selected]
	}
	return m.dashboard.Sagas[0]
}

func (m Model) trace() string {
	if strings.TrimSpace(m.traceID) != "" {
		return m.traceID
	}
	return "tr_tui"
}

func (m Model) readAction(id, label, keyName, command, summary string) Action {
	return Action{ID: id, Label: label, Key: keyName, Command: command, Mutating: false, Allowed: true, Summary: summary}
}

func (m Model) mutatingAction(id, label, keyName, command, summary string, risk schema.Risk, reversibility schema.Reversibility) Action {
	return Action{ID: id, Label: label, Key: keyName, Command: command, Mutating: true, Allowed: !m.readOnly, Summary: summary, Risk: risk, Reversibility: reversibility}
}

func firstCurrentStep(saga client.SagaSummary) string {
	if len(saga.CurrentSteps) == 0 {
		return ""
	}
	return saga.CurrentSteps[0]
}

func newSpinner(noColor bool) spinner.Model {
	style := lipgloss.NewStyle()
	if !noColor {
		style = style.Foreground(lipgloss.Color("45")).Bold(true)
	}
	return spinner.New(spinner.WithSpinner(spinner.Points), spinner.WithStyle(style))
}

func newHelpModel(noColor bool, width int) help.Model {
	h := help.New()
	h.Width = width
	h.ShortSeparator = "  "
	h.FullSeparator = "    "
	if noColor {
		plain := lipgloss.NewStyle()
		h.Styles.ShortKey = plain
		h.Styles.ShortDesc = plain
		h.Styles.ShortSeparator = plain
		h.Styles.FullKey = plain
		h.Styles.FullDesc = plain
		h.Styles.FullSeparator = plain
		h.Styles.Ellipsis = plain
		return h
	}
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	h.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	h.Styles.FullKey = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	h.Styles.FullSeparator = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	h.Styles.Ellipsis = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	return h
}
