package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary   = lipgloss.Color("63")  // purple
	colorSecondary = lipgloss.Color("241") // grey

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

	styleHelp = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))
)
