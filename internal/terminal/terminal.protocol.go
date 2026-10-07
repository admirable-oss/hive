package terminal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"

	"github.com/admirable-oss/hive/internal/protocol"
)

func RegisterHandlers(svc protocol.Service, termService Service) {
	_ = svc.Register("terminal.start", NewStartHandler(termService))
	_ = svc.Register("terminal.attach", NewAttachHandler(termService))
	_ = svc.Register("terminal.input", NewInputHandler(termService))
	_ = svc.Register("terminal.resize", NewResizeHandler(termService))
}

// ─── terminal.start ────────────────────────────────────────────────────────

type StartHandler struct{ service Service }

func NewStartHandler(service Service) *StartHandler {
	return &StartHandler{service: service}
}

func (h *StartHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	var params struct {
		ProcessID  string   `json:"process_id"`
		ID         string   `json:"id"`
		Command    string   `json:"command"`
		Args       []string `json:"args"`
		WorkingDir string   `json:"working_dir"`
		Width      uint16   `json:"width"`
		Height     uint16   `json:"height"`
	}
	b, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(b, &params); err != nil {
		return errResponse(req, "invalid_params", "invalid params")
	}
	procID := params.ProcessID
	if procID == "" {
		procID = params.ID
	}
	if procID == "" || params.Command == "" {
		return errResponse(req, "invalid_params", "process_id and command required")
	}

	size := Size{Width: params.Width, Height: params.Height}
	if size.Width == 0 || size.Height == 0 {
		size = DefaultSize
	}

	cmd := Command{
		Path:       params.Command,
		Args:       params.Args,
		WorkingDir: params.WorkingDir,
		Size:       size,
	}

	sess, err := h.service.Open(ctx, procID, cmd)
	if err != nil {
		return errResponse(req, "internal_error", err.Error())
	}

	result, _ := json.Marshal(map[string]any{
		"process_id": procID,
		"pid":        sess.Pid(),
	})
	return protocol.Response{
		Version: req.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result:  result,
	}
}

// ─── terminal.attach ───────────────────────────────────────────────────────
// After sending the ACK, the handler hijacks the net.Conn and pipes
// PTY output → conn and conn → PTY input until either side closes.

type AttachHandler struct{ service Service }

func NewAttachHandler(service Service) *AttachHandler {
	return &AttachHandler{service: service}
}

func (h *AttachHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	var params struct {
		ProcessID string `json:"process_id"`
		ID        string `json:"id"`
	}
	b, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(b, &params); err != nil {
		return errResponse(req, "invalid_params", "invalid params")
	}
	procID := params.ProcessID
	if procID == "" {
		procID = params.ID
	}
	if procID == "" {
		return errResponse(req, "invalid_params", "process_id required")
	}

	sess, err := h.service.Get(procID)
	if err != nil {
		return errResponse(req, "not_found", err.Error())
	}

	protocol.RegisterHijack(req.ID, func(ctx context.Context, conn net.Conn) {
		subCh, hist, detach := sess.Subscribe()
		defer detach()

		if len(hist) > 0 {
			_, _ = conn.Write(hist)
		}

		done := make(chan struct{}, 2)

		// PTY → client
		go func() {
			for chunk := range subCh {
				if _, err := conn.Write(chunk); err != nil {
					break
				}
			}
			done <- struct{}{}
		}()

		// client → PTY
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := conn.Read(buf)
				if n > 0 {
					if _, wErr := sess.Write(buf[:n]); wErr != nil {
						break
					}
				}
				if err != nil {
					break
				}
			}
			done <- struct{}{}
		}()

		select {
		case <-done:
		case <-ctx.Done():
		}

		_ = conn.Close()
	})

	result, _ := json.Marshal(map[string]string{"status": "attached"})
	return protocol.Response{
		Version: req.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result:  result,
	}
}

// ─── terminal.input ────────────────────────────────────────────────────────

type InputHandler struct{ service Service }

func NewInputHandler(service Service) *InputHandler {
	return &InputHandler{service: service}
}

func (h *InputHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	var params struct {
		ProcessID string `json:"process_id"`
		ID        string `json:"id"`
		Data      string `json:"data"`
	}
	b, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(b, &params); err != nil {
		return errResponse(req, "invalid_params", "invalid params")
	}
	procID := params.ProcessID
	if procID == "" {
		procID = params.ID
	}
	if procID == "" {
		return errResponse(req, "invalid_params", "process_id required")
	}

	raw, err := base64.StdEncoding.DecodeString(params.Data)
	if err != nil {
		raw = []byte(params.Data)
	}

	sess, err := h.service.Get(procID)
	if err != nil {
		return errResponse(req, "not_found", err.Error())
	}

	if _, err := sess.Write(raw); err != nil {
		return errResponse(req, "internal_error", err.Error())
	}

	return protocol.Response{
		Version: req.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result:  json.RawMessage(`{}`),
	}
}

// ─── terminal.resize ───────────────────────────────────────────────────────

type ResizeHandler struct{ service Service }

func NewResizeHandler(service Service) *ResizeHandler {
	return &ResizeHandler{service: service}
}

func (h *ResizeHandler) Handle(ctx context.Context, req protocol.Request) protocol.Response {
	var params struct {
		ProcessID string `json:"process_id"`
		ID        string `json:"id"`
		Width     uint16 `json:"width"`
		Height    uint16 `json:"height"`
	}
	b, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(b, &params); err != nil {
		return errResponse(req, "invalid_params", "invalid params")
	}
	procID := params.ProcessID
	if procID == "" {
		procID = params.ID
	}
	if procID == "" {
		return errResponse(req, "invalid_params", "process_id required")
	}

	sess, err := h.service.Get(procID)
	if err != nil {
		return errResponse(req, "not_found", err.Error())
	}

	if err := sess.Resize(Size{Width: params.Width, Height: params.Height}); err != nil {
		return errResponse(req, "internal_error", err.Error())
	}

	return protocol.Response{
		Version: req.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Result:  json.RawMessage(`{}`),
	}
}

// ─── helpers ───────────────────────────────────────────────────────────────

func errResponse(req protocol.Request, code, msg string) protocol.Response {
	return protocol.Response{
		Version: req.Version,
		Type:    protocol.MessageTypeResponse,
		ID:      req.ID,
		Error:   &protocol.Error{Code: code, Message: msg},
	}
}
