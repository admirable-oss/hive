package client

type Module struct {
	Client Client
}

func NewModule(config Config) *Module {
	return &Module{
		Client: NewService(config),
	}
}
