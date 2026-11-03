package project_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/admirable-oss/hive/internal/project"
)

func writeFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const reviewWorkflow = "version = 1\n[[step]]\nid = \"a\"\nrun = [\"true\"]\n"

func TestGatesWorkflowsAndPlugins(t *testing.T) {
	ctx := context.Background()
	svc, w, dir := newService(t, `version = 1
plugins = ["pr-status"]
workflows = [".hive/workflows"]
[[gate]]
name = "test"
command = ["go", "test", "./..."]
timeout = "10m"
`)
	wf := writeFile(t, dir, ".hive/workflows/review.toml", reviewWorkflow)
	writeFile(t, dir, ".hive/workflows/notes.txt", "not a workflow")

	// Nothing is brought while the project is untrusted.
	if files := svc.WorkflowFiles(); len(files) != 0 {
		t.Fatalf("workflows of an untrusted project: %+v", files)
	}
	if gates, _ := svc.Gates(dir); gates != nil {
		t.Fatalf("gates of an untrusted project: %+v", gates)
	}
	if _, err := svc.Trust(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
	files := svc.WorkflowFiles()
	sum := sha256.Sum256([]byte(reviewWorkflow))
	if len(files) != 1 || files[0].Path != wf || files[0].Digest != hex.EncodeToString(sum[:]) || files[0].Project != dir {
		t.Fatalf("workflow files = %+v", files)
	}
	gates, root := svc.Gates(dir)
	if root != dir || len(gates) != 1 || gates[0].Name != "test" || gates[0].TimeoutMS != 600_000 || !slices.Equal(gates[0].Command, []string{"go", "test", "./..."}) {
		t.Fatalf("gates = %+v at %s", gates, root)
	}
	if gates, _ := svc.Gates(t.TempDir()); gates != nil {
		t.Errorf("gates for another repository: %+v", gates)
	}

	// A missing plugin is installed by the client first; up refuses
	// without it.
	plan, err := svc.Plan(ctx, dir, false, nil)
	if err != nil || !slices.Equal(plan.Plugins, []string{"pr-status"}) || len(plan.Problems) != 0 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if _, err := svc.Up(ctx, dir, nil); !errors.Is(err, project.ErrState) || !strings.Contains(err.Error(), "pr-status") {
		t.Fatalf("up without the plugin: %v", err)
	}
	w.st.Plugins = []string{"pr-status"}
	if _, err := svc.Up(ctx, dir, nil); err != nil {
		t.Fatalf("up with the plugin: %v", err)
	}

	// The workflow is trusted with the file: changing it untrusts both.
	writeFile(t, dir, ".hive/workflows/review.toml", reviewWorkflow+"# and run something else\n")
	if p, _ := svc.Get(ctx, dir); p.Trusted {
		t.Error("the project is trusted after its workflow changed")
	}
	if files := svc.WorkflowFiles(); len(files) != 0 {
		t.Errorf("workflows after a change: %+v", files)
	}
	if gates, _ := svc.Gates(dir); gates != nil {
		t.Errorf("gates after a change: %+v", gates)
	}
}

func TestParseMoreRejects(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"gate without name":    {"version = 1\n[[gate]]\ncommand = [\"x\"]", "name is required"},
		"gate without command": {"version = 1\n[[gate]]\nname = \"g\"", "command is required"},
		"gate twice":           {"version = 1\n[[gate]]\nname = \"g\"\ncommand = [\"x\"]\n[[gate]]\nname = \"g\"\ncommand = [\"y\"]", "used twice"},
		"gate timeout":         {"version = 1\n[[gate]]\nname = \"g\"\ncommand = [\"x\"]\ntimeout = \"2d\"", "1s to 24h"},
		"workflows outside":    {"version = 1\nworkflows = [\"../x\"]", "inside the repository"},
		"plugin id":            {"version = 1\nplugins = [\"Not An ID\"]", "not a plugin ID"},
		"toolchain":            {"version = 1\ntoolchain = \"conda\"", "one of none, auto, nix"},
		"env toolchain":        {"version = 1\n[[environment]]\nid = \"b\"\npath = \"b\"\ntoolchain = \"x\"", "toolchain"},
		"secret scheme":        {"version = 1\n[secrets]\nA = \"hunter2\"", "a reference to your secret store"},
		"secret service":       {"version = 1\n[secrets]\nA = \"secret-service:nokey\"", "attr=value"},
		"op path":              {"version = 1\n[secrets]\nA = \"op://vault\"", "op://<vault>/<item>/<field>"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := project.Parse([]byte(tc.text), nil, root)
			if err == nil || !errors.Is(err, project.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want ErrInvalid with %q", err, tc.want)
			}
		})
	}
	for _, ref := range []string{"keychain:shop", "keychain:shop/me", "secret-service:service=shop,user=me", "pass:shop/db", "op://Private/db/url"} {
		if err := project.CheckSecretRef(ref); err != nil {
			t.Errorf("%s: %v", ref, err)
		}
	}
}

// fakeSecrets resolves references from a map, and counts its reads.
type fakeSecrets struct {
	mu     sync.Mutex
	values map[string]string
	reads  []string
}

func (f *fakeSecrets) Resolve(_ context.Context, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, ref)
	v, ok := f.values[ref]
	if !ok {
		return "", errors.New("not in the store")
	}
	return v, nil
}

func TestSecrets(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeFile(t, dir, project.FileName, `version = 1
[env]
required = ["DATABASE_URL", "API_KEY"]
[secrets]
DATABASE_URL = "pass:shop/db"
STRIPE = "op://dev/stripe/key"
[[tab]]
name = "dev"
  [[tab.pane]]
[[agent]]
name = "a"
kind = "codex"
`)
	writeFile(t, dir, project.LocalFileName, "version = 1\n[secrets]\nAPI_KEY = \"keychain:shop-api\"\n")
	w := newFake()
	secrets := &fakeSecrets{values: map[string]string{"pass:shop/db": "postgres://secret", "keychain:shop-api": "k-123", "op://dev/stripe/key": "sk"}}
	svc := project.NewService(project.Config{Dir: filepath.Join(t.TempDir(), "p"), Workspace: w, Secrets: secrets})
	if _, err := svc.Trust(ctx, dir, false); err != nil {
		t.Fatal(err)
	}

	// Required names are met by secrets; a plan reads no store.
	plan, err := svc.Plan(ctx, dir, false, map[string]string{"STRIPE": "from-the-shell"})
	if err != nil || len(plan.Problems) != 0 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if len(secrets.reads) != 0 {
		t.Fatalf("a plan read secrets: %v", secrets.reads)
	}
	for _, a := range plan.Actions {
		if a.Op == project.OpCreateTab && !slices.Equal(a.Secrets, []string{"API_KEY", "DATABASE_URL", "STRIPE"}) {
			t.Errorf("tab's secrets = %v", a.Secrets)
		}
	}

	if _, err := svc.Up(ctx, dir, nil); err != nil {
		t.Fatal(err)
	}
	// Each reference is read once, and goes to the panes, not to the
	// environment's variables.
	slices.Sort(secrets.reads)
	if want := []string{"keychain:shop-api", "op://dev/stripe/key", "pass:shop/db"}; !slices.Equal(secrets.reads, want) {
		t.Errorf("reads = %v, want %v", secrets.reads, want)
	}
	if len(w.envs) != 2 || w.envs[0]["DATABASE_URL"] != "postgres://secret" || w.envs[1]["API_KEY"] != "k-123" {
		t.Errorf("panes' variables = %v", w.envs)
	}
	if v := w.st.Environments[0].Vars; v["DATABASE_URL"] != "" || v["API_KEY"] != "" {
		t.Errorf("a secret in the environment's variables: %v", v)
	}
}

func TestSecretFailureChangesNothing(t *testing.T) {
	ctx := context.Background()
	_, w, dir := newService(t, "version = 1\n[secrets]\nX = \"pass:missing\"\n[[tab]]\nname = \"t\"\n[[tab.pane]]\n")
	svc := project.NewService(project.Config{Dir: filepath.Join(t.TempDir(), "p"), Workspace: w, Secrets: &fakeSecrets{}})
	if _, err := svc.Trust(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Up(ctx, dir, nil)
	if err == nil || !strings.Contains(err.Error(), "secret X (pass:missing)") {
		t.Fatalf("up = %v", err)
	}
	if len(w.calls) != 0 {
		t.Errorf("calls after a failed secret: %v", w.calls)
	}
}

func TestVarsPrecedence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeFile(t, dir, project.FileName, "version = 1\n[vars]\nA = \"file\"\nB = \"file\"\nC = \"file\"\n[env]\noptional = [\"B\", \"C\", \"D\"]\n[secrets]\nC = \"pass:c\"\n")
	writeFile(t, dir, project.LocalFileName, "version = 1\n[vars]\nC = \"local\"\n")
	w := newFake()
	svc := project.NewService(project.Config{Dir: filepath.Join(t.TempDir(), "p"), Workspace: w, Secrets: &fakeSecrets{}})
	if _, err := svc.Trust(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Up(ctx, dir, map[string]string{"B": "shell", "C": "shell", "D": "shell", "E": "not declared"}); err != nil {
		t.Fatal(err)
	}
	got := w.st.Environments[0].Vars
	want := map[string]string{"A": "file", "B": "shell", "C": "local", "D": "shell"}
	if len(got) != len(want) {
		t.Fatalf("vars = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}

// fakeRunner answers toolchains' commands.
type fakeRunner struct {
	mu   sync.Mutex
	runs [][]string
	env  map[string]string
}

func (f *fakeRunner) run(_ context.Context, _ string, argv []string, _ []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, argv)
	if argv[0] == "devcontainer" {
		return []byte(`{"outcome":"success"}`), nil
	}
	data, _ := json.Marshal(f.env)
	return append([]byte("direnv: loading .envrc\n"), data...), nil
}

func newToolchains(t *testing.T, r *fakeRunner, installed ...string) *project.Toolchains {
	t.Helper()
	return project.NewToolchains(project.ToolchainConfig{
		Dir: t.TempDir(), EnvCommand: []string{"hive", "__env"}, Run: r.run,
		LookPath: func(p string) (string, error) {
			if slices.Contains(installed, p) {
				return "/bin/" + p, nil
			}
			return "", errors.New("not found")
		},
		Environ: func() []string { return []string{"PATH=/usr/bin", "HOME=/home/me", "KEEP=same"} },
	})
}

func TestToolchainsDetectAndCache(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sub := filepath.Join(dir, "api")
	writeFile(t, dir, "mise.toml", "[tools]\ngo = \"1.26\"\n")
	writeFile(t, sub, ".keep", "")
	r := &fakeRunner{env: map[string]string{"PATH": "/mise/go/bin:/usr/bin", "HOME": "/home/me", "KEEP": "same", "GOROOT": "/mise/go", "PWD": "/x", "SHLVL": "2", "HIVE_PANE_ID": "p"}}
	tc := newToolchains(t, r, "mise")

	// Found above the environment, inside the project.
	res := tc.Resolve(project.ToolchainAuto, sub, dir)
	if res.Kind != project.ToolchainMise || res.Dir != dir || res.Cached || res.Problem != "" {
		t.Fatalf("resolve = %+v", res)
	}
	p, err := tc.Prepare(ctx, res)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"PATH": "/mise/go/bin:/usr/bin", "GOROOT": "/mise/go"}; len(p.Env) != len(want) || p.Env["PATH"] != want["PATH"] || p.Env["GOROOT"] != want["GOROOT"] {
		t.Errorf("env = %v, want only what the toolchain changes: %v", p.Env, want)
	}
	if want := []string{"mise", "exec", "--", "hive", "__env"}; !slices.Equal(r.runs[0], want) {
		t.Errorf("ran %q, want %q", r.runs[0], want)
	}

	// Cached by its files: no second run, until they change.
	if res = tc.Resolve(project.ToolchainAuto, sub, dir); !res.Cached {
		t.Fatal("not cached after it was made")
	}
	if _, err := tc.Prepare(ctx, res); err != nil || len(r.runs) != 1 {
		t.Fatalf("prepare from the cache: %v, runs %v", err, r.runs)
	}
	writeFile(t, dir, "mise.toml", "[tools]\ngo = \"1.27\"\n")
	if res = tc.Resolve(project.ToolchainAuto, sub, dir); res.Cached {
		t.Error("still cached after mise.toml changed")
	}

	// An explicit kind that is not there, or not installed, is a problem.
	if res := tc.Resolve(project.ToolchainNix, sub, dir); !strings.Contains(res.Problem, "no flake.nix") {
		t.Errorf("nix without a flake: %+v", res)
	}
	writeFile(t, dir, ".envrc", "use flake\n")
	if res := tc.Resolve(project.ToolchainDirenv, dir, dir); !strings.Contains(res.Problem, "direnv is not installed") {
		t.Errorf("direnv not installed: %+v", res)
	}
	if res := tc.Resolve(project.ToolchainNone, dir, dir); res.Kind != project.ToolchainNone {
		t.Errorf("none = %+v", res)
	}
	// Auto prefers a flake, and finds nothing where there is nothing.
	writeFile(t, dir, "flake.nix", "{}")
	if res := newToolchains(t, r, "nix").Resolve(project.ToolchainAuto, sub, dir); res.Kind != project.ToolchainNix {
		t.Errorf("auto with a flake = %+v", res)
	}
	if res := tc.Resolve(project.ToolchainAuto, t.TempDir(), dir); res.Kind != project.ToolchainNone {
		t.Errorf("auto with nothing = %+v", res)
	}
}

func TestToolchainsInPlans(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeFile(t, dir, project.FileName, "version = 1\ntoolchain = \"auto\"\n[[tab]]\nname = \"t\"\n[[tab.pane]]\ncommand = [\"make\"]\n[[agent]]\nname = \"a\"\nkind = \"codex\"\n")
	writeFile(t, dir, ".devcontainer/devcontainer.json", "{}")
	w := newFake()
	r := &fakeRunner{}
	svc := project.NewService(project.Config{Dir: filepath.Join(t.TempDir(), "p"), Workspace: w, Toolchains: newToolchains(t, r, "devcontainer")})
	if _, err := svc.Trust(ctx, dir, false); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.Plan(ctx, dir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range plan.Actions {
		switch a.Op {
		case project.OpCreateTab:
			if a.Toolchain != project.ToolchainDevcontainer {
				t.Errorf("the tab starts in %q", a.Toolchain)
			}
		case project.OpStartAgent:
			// Agents run on the host: their detection reads host processes.
			if a.Toolchain != "" {
				t.Errorf("the agent starts in %q", a.Toolchain)
			}
		}
	}
	if _, err := svc.Up(ctx, dir, nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"devcontainer", "exec", "--workspace-folder", dir}; len(w.wraps) != 1 || !slices.Equal(w.wraps[0], want) {
		t.Errorf("wraps = %q, want %q", w.wraps, want)
	}
	if len(r.runs) != 1 || r.runs[0][0] != "devcontainer" || r.runs[0][1] != "up" {
		t.Errorf("runs = %q", r.runs)
	}

	// A toolchain that cannot be used is a problem in the plan.
	writeFile(t, dir, project.LocalFileName, "version = 1\ntoolchain = \"nix\"\n")
	plan, _ = svc.Plan(ctx, dir, false, nil)
	if !strings.Contains(strings.Join(plan.Problems, "\n"), "no flake.nix") {
		t.Errorf("problems = %q", plan.Problems)
	}
}

func TestToolStores(t *testing.T) {
	ctx := context.Background()
	var ran [][]string
	stores := project.ToolStores{
		Run: func(_ context.Context, _ string, argv, _ []string) ([]byte, error) {
			ran = append(ran, argv)
			if argv[0] == "pass" {
				return []byte("hunter2\nurl: example.com\n"), nil
			}
			return []byte("value\n"), nil
		},
		LookPath: func(p string) (string, error) {
			if p == "secret-tool" {
				return "", errors.New("not found")
			}
			return "/bin/" + p, nil
		},
	}
	for ref, want := range map[string]struct {
		value string
		argv  []string
	}{
		"pass:shop/db":        {"hunter2", []string{"pass", "show", "shop/db"}},
		"keychain:shop/me":    {"value", []string{"security", "find-generic-password", "-s", "shop", "-w", "-a", "me"}},
		"op://dev/stripe/key": {"value", []string{"op", "read", "--no-newline", "op://dev/stripe/key"}},
	} {
		ran = nil
		v, err := stores.Resolve(ctx, ref)
		if err != nil || v != want.value || len(ran) != 1 || !slices.Equal(ran[0], want.argv) {
			t.Errorf("%s = %q, %v; ran %q", ref, v, err, ran)
		}
	}
	if _, err := stores.Resolve(ctx, "secret-service:service=shop"); err == nil || !strings.Contains(err.Error(), "secret-tool, which is not installed") {
		t.Errorf("missing tool: %v", err)
	}
}

func TestPrintEnv(t *testing.T) {
	t.Setenv("HIVE_TEST_PRINT_ENV", "a\nb")
	var b strings.Builder
	if code := project.PrintEnv(&b); code != 0 {
		t.Fatal(code)
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(b.String())), &env); err != nil || env["HIVE_TEST_PRINT_ENV"] != "a\nb" {
		t.Fatalf("env = %v, %v", env["HIVE_TEST_PRINT_ENV"], err)
	}
}
