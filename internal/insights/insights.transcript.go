package insights

import (
	"bufio"
	"encoding/json"
	"io"
	"strconv"
)

// Transcript is what a session's transcript says it used.
type Transcript struct {
	Tokens Tokens
	Model  string
	// Format is the agent's transcript format: claude or codex.
	Format string
}

// ReadTranscript reads the session totals from an agent's transcript (a
// file of JSON lines), as the agent wrote them:
//
//   - Claude Code: assistant messages' message.usage, each message counted
//     once (its lines repeat the usage);
//   - Codex: the last token_count event's total_token_usage, whose input
//     includes the cached input.
//
// ok is false when the transcript is in neither format: its usage is
// unknown. At most MaxTranscriptBytes are read.
func ReadTranscript(r io.Reader) (t Transcript, ok bool) {
	type claudeUsage struct {
		Input      int64 `json:"input_tokens"`
		Output     int64 `json:"output_tokens"`
		CacheRead  int64 `json:"cache_read_input_tokens"`
		CacheWrite int64 `json:"cache_creation_input_tokens"`
	}
	type codexUsage struct {
		Input  int64 `json:"input_tokens"`
		Cached int64 `json:"cached_input_tokens"`
		Output int64 `json:"output_tokens"`
	}
	var line struct {
		Type    string `json:"type"`
		Message *struct {
			ID    string       `json:"id"`
			Model string       `json:"model"`
			Usage *claudeUsage `json:"usage"`
		} `json:"message"`
		Payload *struct {
			Type  string `json:"type"`
			Model string `json:"model"`
			Info  *struct {
				Total *codexUsage `json:"total_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	claude := map[string]claudeUsage{}
	var order []string
	var codex *codexUsage
	sc := bufio.NewScanner(io.LimitReader(r, MaxTranscriptBytes))
	sc.Buffer(make([]byte, 256<<10), 16<<20)
	for sc.Scan() {
		line.Type, line.Message, line.Payload = "", nil, nil
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		switch {
		case line.Type == "assistant" && line.Message != nil && line.Message.Usage != nil:
			m := line.Message
			if m.Model != "" && m.Model != "<synthetic>" {
				t.Model = m.Model
			}
			id := m.ID
			if id == "" {
				id = "#" + strconv.Itoa(len(order)) // no ID: each line is its own message
			}
			prev, seen := claude[id]
			if !seen {
				order = append(order, id)
			}
			u := *m.Usage
			claude[id] = claudeUsage{
				Input: max(prev.Input, u.Input), Output: max(prev.Output, u.Output),
				CacheRead: max(prev.CacheRead, u.CacheRead), CacheWrite: max(prev.CacheWrite, u.CacheWrite),
			}
		case line.Payload != nil && line.Payload.Type == "token_count" && line.Payload.Info != nil && line.Payload.Info.Total != nil:
			c := *line.Payload.Info.Total
			codex = &c
		case line.Type == "turn_context" && line.Payload != nil && line.Payload.Model != "":
			t.Model = line.Payload.Model
		}
	}
	switch {
	case len(claude) > 0:
		for _, id := range order {
			u := claude[id]
			t.Tokens = t.Tokens.add(Tokens(u))
		}
		t.Format = "claude"
		return t, true
	case codex != nil:
		cached := min(codex.Cached, codex.Input)
		t.Tokens = Tokens{Input: codex.Input - cached, CacheRead: cached, Output: codex.Output}
		t.Format = "codex"
		return t, true
	}
	return Transcript{}, false
}
