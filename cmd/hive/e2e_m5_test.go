package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCodex draws Codex's screens. Each prompt (a bracketed paste and
// Enter) is worked on for a second, written to result.txt in the agent's
// directory, and answered with "DONE: <prompt> in <directory>".
const fakeCodex = `#!/bin/sh
bottom='\033[999;1H'
idle() {
	printf "\033[H\033[2J  >_ OpenAI Codex (fake)\r\n%s${bottom}\033[2A› Ask Codex to do anything${bottom}  ? for shortcuts" "$1"
}
printf '\033[?2004h'
idle ""
while IFS= read -r line; do
	task=$(printf '%s' "$line" | tr -d '\033' | sed -e 's/\[200~//' -e 's/\[201~//')
	printf "\033[H\033[2J  >_ OpenAI Codex (fake)\r\n› %s${bottom}\033[3A• Working (0s • esc to interrupt)" "$task"
	sleep 1
	printf '%s\n' "$task" > result.txt
	idle "› $task\r\n• DONE: $task in $(basename "$PWD")"
done
`

// mcpClient speaks MCP to `hive mcp` over its stdin and stdout, as Claude
// Code would.
type mcpClient struct {
	t   *testing.T
	in  io.WriteCloser
	out *bufio.Scanner
	id  int
}

func (c *cli) mcp() *mcpClient {
	c.t.Helper()
	cmd := exec.Command(os.Args[0], "mcp")
	cmd.Env = append(os.Environ(),
		"HIVE_CLI_TEST_EXEC=1", "HIVE_HOME="+c.home, "HIVE_CONFIG="+c.cfg, "GORACE=atexit_sleep_ms=0",
		// As inside a pane: the socket of the session the caller runs in.
		"HIVE_SOCKET_PATH="+filepath.Join(c.home, "hive.sock"),
	)
	cmd.Env = append(cmd.Env, c.env...)
	in, err := cmd.StdinPipe()
	if err != nil {
		c.t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		c.t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		_ = in.Close()
		if err := cmd.Wait(); err != nil {
			c.t.Errorf("hive mcp: %v\n%s", err, stderr.String())
		}
	})
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	m := &mcpClient{t: c.t, in: in, out: sc}
	init := m.request("initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "fake-claude", "version": "0"},
	})
	if init["serverInfo"].(map[string]any)["name"] != "hive" {
		c.t.Fatalf("initialize: %v", init)
	}
	m.notify("notifications/initialized")
	return m
}

func (m *mcpClient) notify(method string) {
	m.t.Helper()
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if _, err := m.in.Write(append(data, '\n')); err != nil {
		m.t.Fatal(err)
	}
}

func (m *mcpClient) request(method string, params any) map[string]any {
	m.t.Helper()
	m.id++
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.id, "method": method, "params": params})
	if _, err := m.in.Write(append(data, '\n')); err != nil {
		m.t.Fatal(err)
	}
	if !m.out.Scan() {
		m.t.Fatalf("%s: no answer (%v)", method, m.out.Err())
	}
	var resp struct {
		ID     int            `json:"id"`
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(m.out.Bytes(), &resp); err != nil {
		m.t.Fatalf("%s: %v: %s", method, err, m.out.Bytes())
	}
	if resp.ID != m.id || resp.Error != nil {
		m.t.Fatalf("%s: answer %s", method, m.out.Bytes())
	}
	return resp.Result
}

// call runs a tool and returns its text; a tool error fails the test.
func (m *mcpClient) call(tool string, args map[string]any) string {
	m.t.Helper()
	res := m.request("tools/call", map[string]any{"name": tool, "arguments": args})
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if res["isError"] == true {
		m.t.Fatalf("%s %v: %s", tool, args, text)
	}
	return text
}

func (m *mcpClient) callJSON(tool string, args map[string]any, v any) {
	m.t.Helper()
	text := m.call(tool, args)
	if err := json.Unmarshal([]byte(text), v); err != nil {
		m.t.Fatalf("%s: %v: %s", tool, err, text)
	}
}

// TestM5AgentsDriveAgentsOverMCP is M5's acceptance: an agent inside Hive
// spawns 3 Codex agents in worktrees, prompts them, waits for them and
// collects their output, using only MCP.
func TestM5AgentsDriveAgentsOverMCP(t *testing.T) {
	c := newCLI(t)
	c.withShell()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(fakeCodex), 0o755); err != nil {
		t.Fatal(err)
	}
	c.env = append(c.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := filepath.Join(t.TempDir(), "proj")
	gitRun(t, "", "init", "-q", "-b", "main", repo)
	gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	c.must("env", "create", "proj", "--cwd", repo)

	m := c.mcp()
	var tools struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	raw, _ := json.Marshal(m.request("tools/list", nil))
	_ = json.Unmarshal(raw, &tools)
	if len(tools.Tools) < 10 {
		t.Fatalf("tools: %s", raw)
	}

	var envs []struct{ ID string }
	m.callJSON("env_list", nil, &envs)
	if len(envs) != 1 || envs[0].ID != "proj" {
		t.Fatalf("env_list: %+v", envs)
	}

	type started struct {
		ID, Path, Kind string
		Environment    string `json:"environment_id"`
	}
	var agents []started
	for i := range 3 {
		var s started
		m.callJSON("agent_start", map[string]any{
			"kind": "codex", "environment_id": "proj", "worktree": fmt.Sprintf("task-%d", i), "name": fmt.Sprintf("worker-%d", i),
		}, &s)
		if s.ID == "" || s.Kind != "codex" || !strings.Contains(s.Path, "task-"+fmt.Sprint(i)) {
			t.Fatalf("agent_start: %+v", s)
		}
		agents = append(agents, s)
	}
	// All three start working before any is waited for.
	for i, a := range agents {
		var res struct{ Outcome string }
		m.callJSON("agent_prompt", map[string]any{"id": a.ID, "text": fmt.Sprintf("task number %d", i)}, &res)
		if res.Outcome != "started" {
			t.Fatalf("agent_prompt %d: %+v", i, res)
		}
	}
	for i, a := range agents {
		var res struct {
			Outcome string
			Agent   struct {
				State         string
				CompletionSeq uint64 `json:"completion_seq"`
			}
		}
		m.callJSON("agent_wait", map[string]any{"id": a.ID, "until": "done", "timeout_seconds": 60}, &res)
		if res.Outcome != "reached" || res.Agent.CompletionSeq != 1 {
			t.Fatalf("agent_wait %d: %+v", i, res)
		}
		out := m.call("agent_read", map[string]any{"id": a.ID, "source": "recent-unwrapped"})
		want := fmt.Sprintf("DONE: task number %d in task-%d", i, i)
		if !strings.Contains(out, want) {
			t.Fatalf("agent_read %d: %q does not say %q", i, out, want)
		}
		data, err := os.ReadFile(filepath.Join(a.Path, "result.txt"))
		if err != nil || strings.TrimSpace(string(data)) != fmt.Sprintf("task number %d", i) {
			t.Fatalf("agent %d's worktree: %q %v", i, data, err)
		}
	}

	// A prompt with wait does the whole turn in one call.
	var res struct {
		Outcome string
		Output  []string
	}
	start := time.Now()
	m.callJSON("agent_prompt", map[string]any{"id": agents[0].ID, "text": "again", "wait": true, "read_lines": 40}, &res)
	if res.Outcome != "reached" || !strings.Contains(strings.Join(res.Output, "\n"), "DONE: again") {
		t.Fatalf("agent_prompt wait: %+v", res)
	}
	t.Logf("a waited prompt took %s", time.Since(start).Round(time.Millisecond))
}

// TestM5AgentCommands drives a fake Claude Code through the CLI: start,
// prompt, wait, a permission dialog, and the exit statuses scripts see.
func TestM5AgentCommands(t *testing.T) {
	c := newCLI(t)
	c.withShell()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	c.env = append(c.env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c.must("env", "create", "dev", "--managed")

	p := strings.TrimSpace(c.must("agent", "start", "--kind", "claude", "--env", "dev", "--name", "helper"))
	if a := c.agent(p); a.Kind != "claude" || a.PaneID != p {
		t.Fatalf("started %+v", a)
	}

	// The fake takes the prompt and works until it gets another line.
	c.must("agent", "prompt", p, "write the tests")
	if _, _, code := c.run("agent", "wait", p, "--until", "done", "--timeout", "300ms"); code != exitTimeout {
		t.Fatalf("a wait that times out exits %d, want %d", code, exitTimeout)
	}
	c.must("agent", "send-keys", p, "Enter") // now it asks for permission
	if _, _, code := c.run("agent", "wait", p, "--until", "done"); code != exitAgent {
		t.Fatalf("waiting on a blocked agent exits %d, want %d", code, exitAgent)
	}
	if _, errOut, code := c.run("agent", "prompt", p, "do it anyway"); code != exitError || !strings.Contains(errOut, "blocked") {
		t.Fatalf("prompting a blocked agent: exit %d, %q", code, errOut)
	}
	c.must("agent", "send-keys", p, "Enter") // approve: it finishes
	out := c.must("--json", "agent", "wait", p, "--until", "done")
	var res struct {
		Outcome string
		Agent   struct{ State string }
	}
	decode(t, out, &res)
	if res.Outcome != "reached" || res.Agent.State != "done" {
		t.Fatalf("wait: %s", out)
	}
	if read := c.must("agent", "read", p); !strings.Contains(read, "Done.") {
		t.Fatalf("read: %q", read)
	}
	c.must("agent", "rename", p, "tester")
	c.must("agent", "focus", p)
	if a := c.agent(p); a.Name != "tester" || a.State != "idle" { // focusing it marks it seen
		t.Fatalf("after rename and focus: %+v", a)
	}
	c.must("agent", "stop", p)
	c.waitAgent(p, "exited")
}
