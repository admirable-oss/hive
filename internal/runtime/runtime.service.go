package runtime

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

type ServiceImpl struct {
	config   Config
	listener net.Listener
	model    RuntimeModel
}

func NewService(config Config) *ServiceImpl {
	return &ServiceImpl{
		config: config,
		model: RuntimeModel{
			Status: StatusStopped,
		},
	}
}

func (s *ServiceImpl) Start(ctx context.Context) error {
	if err := s.prepareSocket(); err != nil {
		return err
	}

	listener, err := s.config.Listener.Listen(
		"unix",
		s.config.SocketPath,
	)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	s.listener = listener

	s.model.Status = StatusRunning
	s.model.Socket = s.config.SocketPath
	s.model.StartedAt = time.Now()

	go s.accept(ctx)

	return nil
}

func (s *ServiceImpl) Stop(ctx context.Context) error {
	if s.listener == nil {
		return nil
	}

	s.model.Status = StatusStopping

	err := s.listener.Close()

	if removeErr := os.Remove(s.config.SocketPath); removeErr != nil &&
		!os.IsNotExist(removeErr) {
		return removeErr
	}

	s.model.Status = StatusStopped

	return err
}

func (s *ServiceImpl) Runtime(
	ctx context.Context,
) (RuntimeModel, error) {
	return s.model, nil
}

func (s *ServiceImpl) accept(ctx context.Context) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}

		go s.handleConnection(ctx, conn)
	}
}

func (s *ServiceImpl) handleConnection(
	ctx context.Context,
	conn net.Conn,
) {
	defer conn.Close()

	_, _ = conn.Write([]byte("hive runtime\n"))
}

func (s *ServiceImpl) prepareSocket() error {
	if err := os.MkdirAll(
		filepath.Dir(s.config.SocketPath),
		0700,
	); err != nil {
		return err
	}

	if err := os.Remove(s.config.SocketPath); err != nil &&
		!os.IsNotExist(err) {
		return err
	}

	return nil
}