package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryanmoreau/webhook-gateway/internal/deadletter"
)

type dlFocus int

const (
	focusTable dlFocus = iota
	focusPreview
)

var (
	dlHeader  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	dlHelp    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	dlStatus  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	dlConfirm = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	dlKey     = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true).Width(16)
	dlVal     = lipgloss.NewStyle()
	dlErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dlDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	dlBody    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	dlHdrKey  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	dlHdrVal  = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	dlSuccess = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))

	dlPanelActive = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(lipgloss.Color("63"))

	dlPanelInactive = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderTop(true).
				BorderForeground(lipgloss.Color("236"))
)

type deadLettersModel struct {
	dlDir   string
	entries []EntrySummary
	table   table.Model
	preview viewport.Model
	detail  *deadletter.Entry
	lastIdx int
	focus   dlFocus
	confirm string
	status  string
	width   int
	height  int
	ready   bool
}

func newDeadLetters(dlDir string) deadLettersModel {
	cols := []table.Column{
		{Title: "Timestamp", Width: 20},
		{Title: "Request ID", Width: 14},
		{Title: "Route", Width: 18},
		{Title: "Error", Width: 40},
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithFocused(true),
	)
	s := table.DefaultStyles()
	s.Header = s.Header.Bold(true).Foreground(lipgloss.Color("63")).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(lipgloss.Color("236"))
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(true)
	t.SetStyles(s)

	return deadLettersModel{
		dlDir:   dlDir,
		table:   t,
		lastIdx: -1,
		focus:   focusTable,
	}
}

// Messages

type dlListMsg struct {
	entries []EntrySummary
}

type dlPreviewMsg struct {
	idx   int
	entry deadletter.Entry
	err   error
}

type dlRetryResultMsg struct {
	requestID string
	err       error
}

type dlDeleteResultMsg struct {
	requestID string
	err       error
}

type dlRetryAllResultMsg struct {
	succeeded int
	failed    int
}

type dlDeleteAllResultMsg struct {
	succeeded int
	failed    int
}

type dlCurlCopiedMsg struct {
	err error
}

func (m deadLettersModel) fetchList() tea.Msg {
	entries, _ := ListEntries(m.dlDir)
	return dlListMsg{entries: entries}
}

func (m deadLettersModel) loadPreview(idx int, path string) tea.Cmd {
	return func() tea.Msg {
		e, err := ReadEntry(path)
		return dlPreviewMsg{idx: idx, entry: e, err: err}
	}
}

func (m deadLettersModel) update(msg tea.Msg) (deadLettersModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		tableH := max((msg.Height-12)/2, 4)
		previewH := max(msg.Height-12-tableH, 4)
		m.table.SetWidth(msg.Width - 4)
		m.table.SetHeight(tableH)
		if !m.ready {
			m.preview = viewport.New(msg.Width-4, previewH)
			m.preview.SetContent(dlDim.Render("  Loading..."))
			m.ready = true
		} else {
			m.preview.Width = msg.Width - 4
			m.preview.Height = previewH
		}
		if m.detail != nil {
			m.preview.SetContent(renderPreview(*m.detail))
		}

	case dlListMsg:
		m.entries = msg.entries
		m.rebuildTable()
		if len(m.entries) > 0 {
			idx := m.table.Cursor()
			if idx >= 0 && idx < len(m.entries) {
				if m.ready && m.detail == nil {
					m.preview.SetContent(dlDim.Render("  Loading..."))
				}
				return m, m.loadPreview(idx, m.entries[idx].Path)
			}
		} else {
			m.detail = nil
			m.lastIdx = -1
			if m.ready {
				m.preview.SetContent(dlDim.Render("  No dead letters."))
			}
		}

	case dlPreviewMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Error loading: %s", msg.err)
			return m, nil
		}
		m.detail = &msg.entry
		m.lastIdx = msg.idx
		m.updateTableRow(msg.idx, msg.entry)
		if m.ready {
			m.preview.SetContent(renderPreview(msg.entry))
			m.preview.GotoTop()
		}

	case dlRetryResultMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Retry failed [%s]: %s", truncate(msg.requestID, 8), msg.err)
		} else {
			m.status = dlSuccess.Render(fmt.Sprintf("Retried %s", truncate(msg.requestID, 8)))
		}
		return m, m.fetchList

	case dlDeleteResultMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Delete failed [%s]: %s", truncate(msg.requestID, 8), msg.err)
		} else {
			m.status = fmt.Sprintf("Deleted %s", truncate(msg.requestID, 8))
		}
		return m, m.fetchList

	case dlRetryAllResultMsg:
		m.status = fmt.Sprintf("Retry all: %d succeeded, %d failed", msg.succeeded, msg.failed)
		return m, m.fetchList

	case dlDeleteAllResultMsg:
		m.status = fmt.Sprintf("Purge all: %d deleted, %d failed", msg.succeeded, msg.failed)
		return m, m.fetchList

	case dlCurlCopiedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Copy failed: %s", msg.err)
		} else {
			m.status = dlSuccess.Render("Copied curl command to clipboard")
		}

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Update table.
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m deadLettersModel) handleKey(msg tea.KeyMsg) (deadLettersModel, tea.Cmd) {
	key := msg.String()

	// Confirmation dialog intercepts everything.
	if m.confirm != "" {
		action := m.confirm
		m.confirm = ""
		if key == "y" || key == "Y" {
			switch action {
			case "delete":
				return m, m.doDelete(m.selectedEntry())
			case "delete-all":
				return m, m.doDeleteAll()
			case "retry-all":
				return m, m.doRetryAll()
			}
		}
		m.status = "Cancelled"
		return m, nil
	}

	// Enter focuses preview, Esc returns to table.
	switch key {
	case "enter":
		if m.focus == focusTable && m.detail != nil {
			m.focus = focusPreview
			m.table.SetStyles(unfocusedTableStyles())
			return m, nil
		}
	case "esc":
		if m.focus == focusPreview {
			m.focus = focusTable
			m.table.SetStyles(focusedTableStyles())
			return m, nil
		}
	}

	// Actions always available regardless of focus.
	switch key {
	case "r":
		entry := m.selectedEntry()
		if entry.Path == "" {
			return m, nil
		}
		m.status = fmt.Sprintf("Retrying %s...", truncate(entry.RequestID, 8))
		return m, m.doRetryFromList(entry)
	case "d":
		entry := m.selectedEntry()
		if entry.Path == "" {
			return m, nil
		}
		m.confirm = "delete"
		m.status = ""
		return m, nil
	case "R":
		if len(m.entries) == 0 {
			return m, nil
		}
		m.confirm = "retry-all"
		return m, nil
	case "D":
		if len(m.entries) == 0 {
			return m, nil
		}
		m.confirm = "delete-all"
		return m, nil
	case "x":
		if m.detail == nil {
			return m, nil
		}
		return m, m.doCopyCurl(*m.detail)
	}

	// Navigation goes to the focused pane.
	if m.focus == focusPreview {
		var cmd tea.Cmd
		m.preview, cmd = m.preview.Update(msg)
		return m, cmd
	}

	// Table navigation — load preview on cursor move.
	switch key {
	case "up", "k", "down", "j":
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, tea.Batch(cmd, m.maybeLoadPreview())
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m deadLettersModel) maybeLoadPreview() tea.Cmd {
	idx := m.table.Cursor()
	if idx == m.lastIdx || idx < 0 || idx >= len(m.entries) {
		return nil
	}
	return m.loadPreview(idx, m.entries[idx].Path)
}

func (m deadLettersModel) selectedEntry() EntrySummary {
	idx := m.table.Cursor()
	if idx < 0 || idx >= len(m.entries) {
		return EntrySummary{}
	}
	return m.entries[idx]
}

func (m deadLettersModel) doRetryFromList(entry EntrySummary) tea.Cmd {
	return func() tea.Msg {
		e, err := ReadEntry(entry.Path)
		if err != nil {
			return dlRetryResultMsg{requestID: entry.RequestID, err: err}
		}
		err = RetryEntry(e, entry.Path)
		return dlRetryResultMsg{requestID: entry.RequestID, err: err}
	}
}

func (m deadLettersModel) doDelete(entry EntrySummary) tea.Cmd {
	return func() tea.Msg {
		err := DeleteEntry(entry.Path)
		return dlDeleteResultMsg{requestID: entry.RequestID, err: err}
	}
}

func (m deadLettersModel) doRetryAll() tea.Cmd {
	return func() tea.Msg {
		entries, _ := ListEntries(m.dlDir)
		var succeeded, failed int
		for _, s := range entries {
			e, err := ReadEntry(s.Path)
			if err != nil {
				failed++
				continue
			}
			if err := RetryEntry(e, s.Path); err != nil {
				failed++
			} else {
				succeeded++
			}
		}
		return dlRetryAllResultMsg{succeeded: succeeded, failed: failed}
	}
}

func (m deadLettersModel) doDeleteAll() tea.Cmd {
	return func() tea.Msg {
		entries, _ := ListEntries(m.dlDir)
		var succeeded, failed int
		for _, s := range entries {
			if err := DeleteEntry(s.Path); err != nil {
				failed++
			} else {
				succeeded++
			}
		}
		return dlDeleteAllResultMsg{succeeded: succeeded, failed: failed}
	}
}

func (m deadLettersModel) doCopyCurl(entry deadletter.Entry) tea.Cmd {
	return func() tea.Msg {
		curl := buildCurl(entry)
		err := copyToClipboard(curl)
		return dlCurlCopiedMsg{err: err}
	}
}

func (m *deadLettersModel) rebuildTable() {
	rows := make([]table.Row, len(m.entries))
	for i, e := range m.entries {
		rows[i] = table.Row{
			e.Timestamp.Format("2006-01-02 15:04:05"),
			truncate(e.RequestID, 12),
			"...",
			"...",
		}
	}
	m.table.SetRows(rows)
}

// updateTableRow populates a single row with data from a loaded entry.
func (m *deadLettersModel) updateTableRow(idx int, entry deadletter.Entry) {
	rows := m.table.Rows()
	if idx < 0 || idx >= len(rows) {
		return
	}
	rows[idx][2] = truncate(entry.RoutePath, 16)
	rows[idx][3] = truncate(entry.ErrorMessage, 38)
	m.table.SetRows(rows)
}

func focusedTableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.Bold(true).Foreground(lipgloss.Color("63")).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(lipgloss.Color("236"))
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(true)
	return s
}

func unfocusedTableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.Bold(true).Foreground(lipgloss.Color("63")).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(lipgloss.Color("236"))
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("250")).
		Background(lipgloss.Color("237"))
	return s
}

func (m deadLettersModel) viewDL() string {
	var b strings.Builder

	// Header
	count := len(m.entries)
	header := fmt.Sprintf("  %d dead letter", count)
	if count != 1 {
		header += "s"
	}
	b.WriteString(dlHeader.Render(header))
	b.WriteString("\n\n")

	if count == 0 {
		b.WriteString("  No dead letters found.\n")
	} else {
		// Table (top pane)
		b.WriteString(m.table.View())
		b.WriteString("\n")

		// Preview separator — styled by focus
		sep := dlPanelInactive
		label := " Preview "
		if m.focus == focusPreview {
			sep = dlPanelActive
			label = " Preview [j/k scroll, esc back] "
		}
		b.WriteString(sep.Render(dlDim.Render(label)))
		b.WriteString("\n")

		if m.ready {
			b.WriteString(m.preview.View())
		}
	}

	// Help bar
	b.WriteString("\n")
	if m.focus == focusPreview {
		b.WriteString(dlHelp.Render("  esc back  r retry  d delete  x curl"))
	} else {
		b.WriteString(dlHelp.Render("  enter preview  r retry  d delete  x curl  R retry all  D purge all"))
	}

	if m.confirm != "" {
		b.WriteString("\n")
		b.WriteString(renderConfirmDL(m.confirm, count))
	}

	if m.status != "" {
		b.WriteString("\n  ")
		b.WriteString(m.status)
	}

	return b.String()
}

func renderConfirmDL(action string, count int) string {
	switch action {
	case "delete":
		return dlConfirm.Render("  Delete this dead letter? [y/N] ")
	case "delete-all":
		return dlConfirm.Render(fmt.Sprintf("  Purge all %d dead letters? [y/N] ", count))
	case "retry-all":
		return dlConfirm.Render(fmt.Sprintf("  Retry all %d dead letters? [y/N] ", count))
	}
	return ""
}

func renderPreview(entry deadletter.Entry) string {
	var b strings.Builder

	field := func(label, value string) {
		fmt.Fprintf(&b, "  %s %s\n", dlKey.Render(label), dlVal.Render(value))
	}

	errField := func(label, value string) {
		fmt.Fprintf(&b, "  %s %s\n", dlKey.Render(label), dlErr.Render(value))
	}

	field("Request ID", entry.RequestID)
	field("Timestamp", entry.Timestamp.Format("2006-01-02 15:04:05 UTC"))
	field("Route", entry.RoutePath)
	field("Destination", entry.DestinationURL)
	errField("Error", entry.ErrorMessage)
	field("Attempts", fmt.Sprintf("%d", entry.AttemptCount))

	if len(entry.Headers) > 0 {
		b.WriteString("\n")
		fmt.Fprintf(&b, "  %s\n", dlKey.Render("Headers"))
		for k, v := range entry.Headers {
			fmt.Fprintf(&b, "    %s  %s\n",
				dlHdrKey.Render(k+":"),
				dlHdrVal.Render(v))
		}
	}

	if len(entry.RequestBody) > 0 {
		b.WriteString("\n")
		fmt.Fprintf(&b, "  %s\n", dlKey.Render("Body"))
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, entry.RequestBody, "    ", "  "); err == nil {
			b.WriteString("    ")
			b.WriteString(dlBody.Render(pretty.String()))
		} else {
			b.WriteString("    ")
			b.WriteString(dlBody.Render(string(entry.RequestBody)))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// buildCurl generates a curl command that replays the dead letter delivery.
func buildCurl(entry deadletter.Entry) string {
	var b strings.Builder
	b.WriteString("curl -X POST")

	for k, v := range entry.Headers {
		b.WriteString(fmt.Sprintf(" \\\n  -H '%s: %s'", shellEscape(k), shellEscape(v)))
	}

	if len(entry.RequestBody) > 0 {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, entry.RequestBody, "", "  "); err == nil {
			b.WriteString(fmt.Sprintf(" \\\n  -d '%s'", shellEscape(pretty.String())))
		} else {
			b.WriteString(fmt.Sprintf(" \\\n  -d '%s'", shellEscape(string(entry.RequestBody))))
		}
	}

	b.WriteString(fmt.Sprintf(" \\\n  '%s'", shellEscape(entry.DestinationURL)))

	return b.String()
}

func shellEscape(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		// Try xclip first, fall back to xsel.
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		}
	default:
		return fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
