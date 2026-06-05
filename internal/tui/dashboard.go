package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryanmoreau/webhook-gateway/internal/stats"
)

var (
	dashSection = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63")).MarginTop(1)
	dashLabel   = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Width(24)
	dashValue   = lipgloss.NewStyle().Bold(true)
	dashOnline  = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	dashOffline = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	dashDots    = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
)

type dashboardModel struct {
	gatewayURL string
	client     *GatewayClient
	stats      stats.Snapshot
	dlDir      string
	dlCount    int
	online     bool
	lastErr    string
	width      int
}

func newDashboard(gatewayURL, dlDir, authToken string) dashboardModel {
	return dashboardModel{
		gatewayURL: gatewayURL,
		client:     NewGatewayClient(gatewayURL, authToken),
		dlDir:      dlDir,
	}
}

type statsResultMsg struct {
	stats stats.Snapshot
	err   error
}

type dlCountMsg struct {
	count int
}

func (m dashboardModel) fetchStats() tea.Msg {
	s, err := m.client.FetchStats()
	return statsResultMsg{stats: s, err: err}
}

func (m dashboardModel) fetchDLCount() tea.Msg {
	entries, _ := ListEntries(m.dlDir)
	return dlCountMsg{count: len(entries)}
}

func (m dashboardModel) update(msg tea.Msg) (dashboardModel, tea.Cmd) {
	switch msg := msg.(type) {
	case statsResultMsg:
		if msg.err != nil {
			m.online = false
			m.lastErr = msg.err.Error()
		} else {
			m.online = true
			m.stats = msg.stats
			m.lastErr = ""
		}
	case dlCountMsg:
		m.dlCount = msg.count
	case tea.WindowSizeMsg:
		m.width = msg.Width
	}
	return m, nil
}

func (m dashboardModel) view() string {
	var b strings.Builder

	status := dashOnline.Render("Online")
	if !m.online {
		status = dashOffline.Render("Offline")
	}
	fmt.Fprintf(&b, "  Gateway: %s  %s\n", m.gatewayURL, status)
	if m.online {
		fmt.Fprintf(&b, "  Uptime:  %s\n", m.stats.Uptime)
	}
	if m.lastErr != "" {
		fmt.Fprintf(&b, "  Error:   %s\n", m.lastErr)
	}

	b.WriteString("\n")
	b.WriteString(dashSection.Render("  Requests"))
	b.WriteString("\n")
	dashRow(&b, "Received", m.stats.RequestsReceived)
	dashRow(&b, "In Flight", m.stats.InFlight)

	b.WriteString("\n")
	b.WriteString(dashSection.Render("  Deliveries"))
	b.WriteString("\n")
	dashRow(&b, "Attempted", m.stats.DeliveriesAttempted)
	dashRow(&b, "Succeeded", m.stats.DeliveriesSucceeded)
	dashRow(&b, "Failed", m.stats.DeliveriesFailed)

	b.WriteString("\n")
	b.WriteString(dashSection.Render("  Security"))
	b.WriteString("\n")
	dashRow(&b, "Signature Failures", m.stats.SignatureFailures)
	dashRow(&b, "Duplicates Skipped", m.stats.DuplicatesSkipped)

	b.WriteString("\n")
	b.WriteString(dashSection.Render("  Dead Letters"))
	b.WriteString("\n")
	dashRow(&b, "Written", m.stats.DeadLettersWritten)
	dashRow(&b, "On Disk", int64(m.dlCount))

	return b.String()
}

func dashRow(b *strings.Builder, label string, value int64) {
	dots := max(22-len(label), 1)
	fmt.Fprintf(b, "    %s %s %s\n",
		dashLabel.Render(label),
		dashDots.Render(strings.Repeat(".", dots)),
		dashValue.Render(fmtInt(value)),
	)
}

// fmtInt formats an integer with comma separators (e.g., 12847 -> "12,847").
func fmtInt(n int64) string {
	if n < 0 {
		return "-" + fmtInt(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
	}
	for i := rem; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
