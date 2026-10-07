package terminal

type Module struct {
	Service Service
}

func NewModule(_ Config) *Module {
	factory := NewPTYFactory()
	service := NewService(factory)
	return &Module{Service: service}
}
