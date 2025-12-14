package main

import (
	"log"

	tea "github.com/charmbracelet/bubbletea"
	_ "github.com/gdamore/tcell/v2" // keep tcell in the build; Bubble Tea already owns the terminal
	"github.com/nexusriot/ducknetview/internal/ui"
)

func main() {
	m := ui.NewModel()

	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
	)

	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
