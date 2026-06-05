package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryanmoreau/webhook-gateway/internal/config"
)

const (
	tabDashboard   = 0
	tabDeadLetters = 1
	tabLogs        = 2
	tabRoutes      = 3

	statsPollInterval = 2 * time.Second
	dlPollInterval    = 5 * time.Second
)

// Options configures the TUI.
type Options struct {
	GatewayURL string
	Config     *config.Config
	DLDir      string
}

type model struct {
	tabs        []string
	activeTab   int
	dashboard   dashboardModel
	deadLetters deadLettersModel
	logs        logsModel
	routes      routesModel
	streamer    *sseStreamer
	width       int
	height      int
}

// programMsg carries the tea.Program reference so the model can use Send().
type programMsg struct {
	p *tea.Program
}

func newModel(opts Options) model {
	return model{
		tabs:        []string{"Dashboard", "Dead Letters", "Logs", "Routes"},
		activeTab:   tabDashboard,
		dashboard:   newDashboard(opts.GatewayURL, opts.DLDir),
		deadLetters: newDeadLetters(opts.DLDir),
		logs:        newLogs(opts.GatewayURL),
		routes:      newRoutes(opts.Config),
		streamer:    newSSEStreamer(opts.GatewayURL),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		tickStats(),
		tickDL(),
		m.dashboard.fetchStats,
		m.dashboard.fetchDLCount,
		m.deadLetters.fetchList,
		m.logs.connectSSE,
	)
}

type statsTickMsg time.Time
type dlTickMsg time.Time

func tickStats() tea.Cmd {
	return tea.Tick(statsPollInterval, func(t time.Time) tea.Msg {
		return statsTickMsg(t)
	})
}

func tickDL() tea.Cmd {
	return tea.Tick(dlPollInterval, func(t time.Time) tea.Msg {
		return dlTickMsg(t)
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case programMsg:
		// Start the SSE streamer now that we have the program reference.
		m.streamer.start(func(tmsg tea.Msg) {
			msg.p.Send(tmsg)
		})
		return m, nil

	case tea.KeyMsg:
		// Let active tab intercept keys before global handling.
		if m.activeTab == tabDeadLetters && isDeadLetterKey(msg.String()) {
			var cmd tea.Cmd
			m.deadLetters, cmd = m.deadLetters.update(msg)
			return m, cmd
		}
		if m.activeTab == tabLogs && isLogKey(msg.String()) {
			var cmd tea.Cmd
			m.logs, cmd = m.logs.update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "ctrl+c", "q":
			m.streamer.stop()
			return m, tea.Quit
		case "tab":
			m.activeTab = (m.activeTab + 1) % len(m.tabs)
			return m, nil
		case "shift+tab":
			m.activeTab = (m.activeTab - 1 + len(m.tabs)) % len(m.tabs)
			return m, nil
		case "1":
			m.activeTab = tabDashboard
			return m, nil
		case "2":
			m.activeTab = tabDeadLetters
			return m, nil
		case "3":
			m.activeTab = tabLogs
			return m, nil
		case "4":
			m.activeTab = tabRoutes
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Broadcast to all tabs so they have dimensions ready when activated.
		m.dashboard, _ = m.dashboard.update(msg)
		m.deadLetters, _ = m.deadLetters.update(msg)
		m.logs, _ = m.logs.update(msg)
		m.routes, _ = m.routes.update(msg)
		return m, nil

	case statsTickMsg:
		return m, tea.Batch(
			m.dashboard.fetchStats,
			m.dashboard.fetchDLCount,
			tickStats(),
		)

	case dlTickMsg:
		return m, tea.Batch(
			m.deadLetters.fetchList,
			tickDL(),
		)
	}

	// Delegate to active tab.
	var cmd tea.Cmd
	switch m.activeTab {
	case tabDashboard:
		m.dashboard, cmd = m.dashboard.update(msg)
	case tabDeadLetters:
		m.deadLetters, cmd = m.deadLetters.update(msg)
	case tabLogs:
		m.logs, cmd = m.logs.update(msg)
	case tabRoutes:
		m.routes, cmd = m.routes.update(msg)
	}
	return m, cmd
}

func isDeadLetterKey(key string) bool {
	switch key {
	case "r", "d", "R", "D", "x", "y", "Y", "n", "N",
		"enter", "esc",
		"up", "down", "k", "j", "pgup", "pgdown":
		return true
	}
	return false
}

func isLogKey(key string) bool {
	switch key {
	case "f", "a", "g", "G", "up", "down", "pgup", "pgdown":
		return true
	}
	return false
}

func (m model) View() string {
	var b strings.Builder

	// Tab bar
	var tabs []string
	for i, t := range m.tabs {
		if i == m.activeTab {
			tabs = append(tabs, styleTabActive.Render(t))
		} else {
			tabs = append(tabs, styleTabInactive.Render(t))
		}
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
	b.WriteString(styleTabBar.Render(tabBar))
	b.WriteString("\n")

	// Active tab content
	switch m.activeTab {
	case tabDashboard:
		b.WriteString(m.dashboard.view())
	case tabDeadLetters:
		b.WriteString(m.deadLetters.viewDL())
	case tabLogs:
		b.WriteString(m.logs.viewLogs())
	case tabRoutes:
		b.WriteString(m.routes.viewRoutes())
	}

	// Global help
	b.WriteString("\n\n")
	b.WriteString(styleHelp.Render("  1/2/3/4 switch tabs  tab next  q quit"))

	return b.String()
}

// Run starts the TUI.
func Run(opts Options) error {
	m := newModel(opts)
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Send the program reference to the model so it can start the SSE streamer.
	go func() {
		// Small delay to ensure the program is running before sending.
		time.Sleep(100 * time.Millisecond)
		p.Send(programMsg{p: p})
	}()

	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return nil
}
