package runtime

type Module struct {
	Service   Service
	ViewModel ViewModelService
}

func NewModule(config Config) *Module {
	service := NewService(config)

	return &Module{
		Service:   service,
		ViewModel: NewViewModel(service),
	}
}