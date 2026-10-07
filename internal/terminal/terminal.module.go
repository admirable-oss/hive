// Package terminal runs commands inside pseudo-terminals so agents that expect
// a real TTY (interactive CLIs, prompts, colours) behave as they would for a
// human. It owns live sessions only; process lifecycle lives in package process.
package terminal

type Module struct {
	Service Service
}

func NewModule() *Module {
	return &Module{Service: NewService(NewPTYFactory())}
}
