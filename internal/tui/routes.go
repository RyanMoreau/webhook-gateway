package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryanmoreau/webhook-gateway/internal/config"
)

var (
	routeHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	routeLabel  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	routeDest   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
)

type routesModel struct {
	cfg      *config.Config
	viewport viewport.Model
	ready    bool
	width    int
	height   int
}

func newRoutes(cfg *config.Config) routesModel {
	return routesModel{cfg: cfg}
}

func (m routesModel) update(msg tea.Msg) (routesModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.viewport = viewport.New(msg.Width-4, max(msg.Height-8, 5))
			if m.cfg != nil {
				m.viewport.SetContent(renderRoutes(m.cfg))
			}
			m.ready = true
		} else {
			m.viewport.Width = msg.Width - 4
			m.viewport.Height = max(msg.Height-8, 5)
		}
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m routesModel) viewRoutes() string {
	if m.cfg == nil {
		return "  No config loaded. Use -config to specify a config file."
	}
	if !m.ready {
		return "  Loading..."
	}

	var b strings.Builder
	count := len(m.cfg.Routes)
	header := fmt.Sprintf("  %d route", count)
	if count != 1 {
		header += "s"
	}
	header += " configured"
	b.WriteString(routeHeader.Render(header))
	b.WriteString("\n\n")
	b.WriteString(m.viewport.View())
	return b.String()
}

func renderRoutes(cfg *config.Config) string {
	var b strings.Builder

	for i, r := range cfg.Routes {
		if i > 0 {
			b.WriteString("\n")
			b.WriteString(strings.Repeat("-", 50))
			b.WriteString("\n\n")
		}

		b.WriteString(routeHeader.Render(fmt.Sprintf("  %s", r.Path)))
		b.WriteString("\n")

		sigDesc := r.Signature.Type
		if r.Signature.Header != "" {
			sigDesc += fmt.Sprintf(" (%s)", r.Signature.Header)
		}
		fmt.Fprintf(&b, "    %s %s\n", routeLabel.Render("Signature:"), sigDesc)

		if r.Idempotency.Enabled {
			idem := fmt.Sprintf("enabled (key: %s", r.Idempotency.KeyPath)
			if r.Idempotency.TTL > 0 {
				idem += fmt.Sprintf(", ttl: %s", r.Idempotency.TTL)
			}
			idem += ")"
			fmt.Fprintf(&b, "    %s %s\n", routeLabel.Render("Idempotency:"), idem)
		}

		fmt.Fprintf(&b, "    %s\n", routeLabel.Render("Destinations:"))
		for _, d := range r.Destinations {
			timeout := ""
			if d.Timeout > 0 {
				timeout = fmt.Sprintf(" (timeout: %s)", d.Timeout)
			}
			fmt.Fprintf(&b, "      %s%s\n", routeDest.Render(d.URL), timeout)
			for k, v := range d.Headers {
				fmt.Fprintf(&b, "        %s: %s\n", k, v)
			}
		}

		retry := fmt.Sprintf("%d attempts, %s %s-%s",
			r.Retry.MaxAttempts, r.Retry.Backoff,
			r.Retry.InitialInterval, r.Retry.MaxInterval)
		fmt.Fprintf(&b, "    %s %s\n", routeLabel.Render("Retry:"), retry)

		if len(r.ForwardHeaders) > 0 {
			fmt.Fprintf(&b, "    %s %s\n",
				routeLabel.Render("Forward:"),
				strings.Join(r.ForwardHeaders, ", "))
		}
	}

	return b.String()
}
