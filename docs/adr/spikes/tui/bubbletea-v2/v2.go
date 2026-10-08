package main

import (
	"io"

	tea2 "charm.land/bubbletea/v2"
	lg2 "charm.land/lipgloss/v2"
)

type stepMsg struct{}
type doneMsg struct{}

type model2 struct {
	g    *grid
	step func(*grid)
	box  lg2.Style
}

func (m model2) Init() tea2.Cmd { return nil }

func (m model2) Update(msg tea2.Msg) (tea2.Model, tea2.Cmd) {
	switch msg.(type) {
	case stepMsg:
		m.step(m.g)
	case doneMsg:
		return m, tea2.Quit
	}
	return m, nil
}

func (m model2) View() tea2.View {
	var rowsOut []string
	for r := range rows {
		var cells []string
		for c := range cols {
			cells = append(cells, m.box.Render(m.g.paneText(r*cols+c)))
		}
		rowsOut = append(rowsOut, lg2.JoinHorizontal(lg2.Top, cells...))
	}
	v := tea2.NewView(lg2.JoinVertical(lg2.Left, rowsOut...))
	v.AltScreen = true
	return v
}

func runV2(out io.Writer, step func(*grid), n int) {
	m := model2{g: newGrid(), step: step, box: lg2.NewStyle().Border(lg2.RoundedBorder()).Width(paneW + 2).Height(paneH + 2).MaxHeight(paneH + 2)}
	p := tea2.NewProgram(m, tea2.WithOutput(out), tea2.WithInput(nil), tea2.WithWindowSize(220, 50), tea2.WithoutSignalHandler(), tea2.WithFPS(120))
	go drive(nil, n, func(v any) { p.Send(v) }, nil)
	if _, err := p.Run(); err != nil {
		panic(err)
	}
}
