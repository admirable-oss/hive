package runtime

import "context"

type ViewModel struct {
	ID     string
	Status string
	Socket string
}

type ViewModelService interface {
	ViewModel(context.Context) (ViewModel, error)
}

type ViewModelImpl struct {
	service Reader
}

func NewViewModel(service Reader) *ViewModelImpl {
	return &ViewModelImpl{
		service: service,
	}
}

func (v *ViewModelImpl) ViewModel(
	ctx context.Context,
) (ViewModel, error) {
	model, err := v.service.Runtime(ctx)
	if err != nil {
		return ViewModel{}, err
	}

	return ViewModel{
		ID:     model.ID,
		Status: string(model.Status),
		Socket: model.Socket,
	}, nil
}