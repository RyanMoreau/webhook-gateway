package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryanmoreau/webhook-gateway/internal/logging"
)

var (
	logHeader    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	logHelp      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	logTime      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	logLevelDBG  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	logLevelINFO = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	logLevelWARN = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	logLevelERR  = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	logMsg       = lipgloss.NewStyle()
	logAttrKey   = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	logAttrVal   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	logFilter    = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
)

type logLevel int

const (
	logAll logLevel = iota
	logDebug
	logInfo
	logWarn
	logError
)

var levelNames = []string{"ALL", "DEBUG", "INFO", "WARN", "ERROR"}
var levelOrder = []logLevel{logAll, logDebug, logInfo, logWarn, logError}

type logsModel struct {
	gatewayURL string
	viewport   viewport.Model
	entries    []logging.LogEntry
	level      logLevel
	connected  bool
	autoScroll bool
	ready      bool
	width      int
	height     int
	maxEntries int
}

func newLogs(gatewayURL string) logsModel {
	return logsModel{
		gatewayURL: gatewayURL,
		level:      logAll,
		autoScroll: true,
		maxEntries: 500,
	}
}

// Messages

type logEntryMsg struct {
	entry logging.LogEntry
}

type logHistoryMsg struct {
	entries []logging.LogEntry
}

type logConnectedMsg struct{}

type logErrorMsg struct {
	err error
}

// Commands

func (m logsModel) connectSSE() tea.Msg {
	resp, err := http.Get(m.gatewayURL + "/logs")
	if err != nil {
		return logErrorMsg{err: err}
	}
	defer resp.Body.Close()

	var entries []logging.LogEntry
	json.NewDecoder(resp.Body).Decode(&entries)
	return logHistoryMsg{entries: entries}
}

// sseStreamer manages a persistent SSE connection and sends entries to the program.
type sseStreamer struct {
	url  string
	done chan struct{}
}

func newSSEStreamer(url string) *sseStreamer {
	return &sseStreamer{
		url:  url,
		done: make(chan struct{}),
	}
}

func (s *sseStreamer) start(send func(tea.Msg)) {
	go func() {
		for {
			select {
			case <-s.done:
				return
			default:
			}

			resp, err := http.Get(s.url + "/logs/stream")
			if err != nil {
				time.Sleep(2 * time.Second)
				continue
			}

			send(logConnectedMsg{})

			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				data := strings.TrimPrefix(line, "data: ")
				var entry logging.LogEntry
				if err := json.Unmarshal([]byte(data), &entry); err != nil {
					continue
				}
				send(logEntryMsg{entry: entry})
			}
			resp.Body.Close()

			// Reconnect after a brief pause.
			select {
			case <-s.done:
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
}

func (s *sseStreamer) stop() {
	close(s.done)
}

func (m logsModel) update(msg tea.Msg) (logsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.viewport = viewport.New(msg.Width-4, max(msg.Height-8, 5))
			m.ready = true
		} else {
			m.viewport.Width = msg.Width - 4
			m.viewport.Height = max(msg.Height-8, 5)
		}
		m.rebuildView()

	case logHistoryMsg:
		m.entries = msg.entries
		m.rebuildView()

	case logEntryMsg:
		m.entries = append(m.entries, msg.entry)
		if len(m.entries) > m.maxEntries {
			m.entries = m.entries[len(m.entries)-m.maxEntries:]
		}
		m.rebuildView()

	case logConnectedMsg:
		m.connected = true

	case logErrorMsg:
		m.connected = false

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.ready {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m logsModel) handleKey(msg tea.KeyMsg) (logsModel, tea.Cmd) {
	switch msg.String() {
	case "f":
		// Cycle through filter levels.
		idx := 0
		for i, l := range levelOrder {
			if l == m.level {
				idx = i
				break
			}
		}
		m.level = levelOrder[(idx+1)%len(levelOrder)]
		m.rebuildView()
		return m, nil
	case "a":
		m.autoScroll = !m.autoScroll
		if m.autoScroll {
			m.viewport.GotoBottom()
		}
		return m, nil
	case "G":
		m.viewport.GotoBottom()
		return m, nil
	case "g":
		m.viewport.GotoTop()
		return m, nil
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *logsModel) rebuildView() {
	if !m.ready {
		return
	}

	var b strings.Builder
	for _, e := range m.entries {
		if !m.matchesLevel(e) {
			continue
		}
		b.WriteString(formatLogEntry(e))
		b.WriteString("\n")
	}

	content := b.String()
	if content == "" {
		content = "  Waiting for logs..."
	}

	m.viewport.SetContent(content)
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m logsModel) matchesLevel(e logging.LogEntry) bool {
	switch m.level {
	case logDebug:
		return true
	case logInfo:
		return e.Level != "DEBUG"
	case logWarn:
		return e.Level == "WARN" || e.Level == "ERROR"
	case logError:
		return e.Level == "ERROR"
	default:
		return true
	}
}

func (m logsModel) viewLogs() string {
	if !m.ready {
		return "  Loading..."
	}

	var b strings.Builder

	// Header with connection status and filter
	status := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render("streaming")
	if !m.connected {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Render("disconnected")
	}
	b.WriteString(logHeader.Render(fmt.Sprintf("  Logs [%s]", status)))

	if m.level != logAll {
		b.WriteString("  ")
		b.WriteString(logFilter.Render(fmt.Sprintf("filter: >=%s", levelNames[m.level])))
	}

	autoLabel := ""
	if m.autoScroll {
		autoLabel = "  [auto-scroll]"
	}
	if autoLabel != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(autoLabel))
	}

	b.WriteString("\n\n")
	b.WriteString(m.viewport.View())
	b.WriteString("\n\n")
	b.WriteString(logHelp.Render("  f filter level  a auto-scroll  g top  G bottom"))

	return b.String()
}

func formatLogEntry(e logging.LogEntry) string {
	ts := logTime.Render(e.Time.Format("15:04:05.000"))

	var lvl string
	switch e.Level {
	case "DEBUG":
		lvl = logLevelDBG.Render("DBG")
	case "INFO":
		lvl = logLevelINFO.Render("INF")
	case "WARN":
		lvl = logLevelWARN.Render("WRN")
	case "ERROR":
		lvl = logLevelERR.Render("ERR")
	default:
		lvl = e.Level
	}

	msg := logMsg.Render(e.Message)

	var attrs strings.Builder
	for k, v := range e.Attrs {
		attrs.WriteString(" ")
		attrs.WriteString(logAttrKey.Render(k))
		attrs.WriteString("=")
		attrs.WriteString(logAttrVal.Render(v))
	}

	return fmt.Sprintf("  %s %s %s%s", ts, lvl, msg, attrs.String())
}
