package main

import (
	"io"

	tea1 "github.com/charmbracelet/bubbletea"
	lg1 "github.com/charmbracelet/lipgloss"
)

type (
	stepMsg struct{}
	doneMsg struct{}
)

type model1 struct {
	g    *grid
	step func(*grid)
	box  lg1.Style
}

func (m model1) Init() tea1.Cmd { return nil }

func (m model1) Update(msg tea1.Msg) (tea1.Model, tea1.Cmd) {
	switch msg.(type) {
	case stepMsg:
		m.step(m.g)
	case doneMsg:
		return m, tea1.Quit
	}
	return m, nil
}

func (m model1) View() string {
	var rowsOut []string
	for r := range rows {
		var cells []string
		for c := range cols {
			cells = append(cells, m.box.Render(m.g.paneText(r*cols+c)))
		}
		rowsOut = append(rowsOut, lg1.JoinHorizontal(lg1.Top, cells...))
	}
	return lg1.JoinVertical(lg1.Left, rowsOut...)
}

func runV1(out io.Writer, step func(*grid), n int) {
	m := model1{g: newGrid(), step: step, box: lg1.NewStyle().Border(lg1.RoundedBorder()).Width(paneW).Height(paneH).MaxHeight(paneH + 2)}
	p := tea1.NewProgram(m, tea1.WithOutput(out), tea1.WithInput(nil), tea1.WithAltScreen(), tea1.WithoutSignalHandler(), tea1.WithFPS(120))
	go drive(p.Send, n, func(v any) { p.Send(v) }, tea1.WindowSizeMsg{Width: 220, Height: 50})
	if _, err := p.Run(); err != nil {
		panic(err)
	}
}
