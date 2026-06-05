package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary   = lipgloss.Color("63")  // purple
	colorSecondary = lipgloss.Color("241") // grey
	colorSuccess   = lipgloss.Color("42")  // green
	colorDanger    = lipgloss.Color("196") // red
	colorWarning   = lipgloss.Color("214") // yellow
	colorMuted     = lipgloss.Color("245")

	styleTabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(colorPrimary).
			Padding(0, 2)

	styleTabInactive = lipgloss.NewStyle().
				Foreground(colorSecondary).
				Padding(0, 2)

	styleTabBar = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(lipgloss.Color("236")).
			MarginBottom(1)

	styleSection = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			MarginTop(1).
			MarginBottom(0)

	styleLabel = lipgloss.NewStyle().
			Foreground(colorMuted).
			Width(24)

	styleValue = lipgloss.NewStyle().
			Bold(true)

	styleOnline = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true)

	styleOffline = lipgloss.NewStyle().
			Foreground(colorDanger).
			Bold(true)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(colorMuted).
			MarginTop(1)

	styleHelp = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	styleDetailKey = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true).
			Width(20)

	styleDetailVal = lipgloss.NewStyle()

	styleConfirm = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWarning)
)
