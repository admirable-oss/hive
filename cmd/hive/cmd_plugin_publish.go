package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/admirable-oss/hive/internal/buildinfo"
	"github.com/admirable-oss/hive/internal/plugin"
	"github.com/admirable-oss/hive/internal/update"
)

// testsSince is the first Hive that reads a manifest's [test] table;
// earlier releases reject the key.
const testsSince = "1.7.0"

func newPluginPublishCmd(a *app) *cobra.Command {
	var key, ref, out, registryDir, remote string
	var push, submit, dryRun, unsigned, skipTests bool
	cmd := &cobra.Command{
		Use:   "publish [dir]",
		Short: "Tag, checksum, sign and record a plugin release for the registry",
		Long: `Publish makes a registry release of the plugin in dir (default: here), in
its git repository:

  1. checks the manifest, and refuses a tree with uncommitted changes or a
     tag that already points at another commit;
  2. runs the plugin's checks (hive plugin test), including its [test]
     command;
  3. tags the commit (v<version>, or <id>/v<version> for a plugin in a
     subdirectory) and pushes the tag with --push;
  4. computes the git archive checksum the registry pins;
  5. signs the release statement with your SSH key (--key, or git config
     user.signingkey; a public key file signs through ssh-agent);
  6. records how it was built (GitHub Actions, or local);
  7. writes the release record (--out, or standard output), and with
     --submit appends it to the plugin's submission in a checkout of the
     registry repository and opens a pull request with gh.

--dry-run does everything but tag, push and submit.`,
		Example: `  hive plugin publish --dry-run
  hive plugin publish --key ~/.ssh/id_ed25519 --push
  hive plugin publish --push --submit --registry ~/src/hive`,
		Args: rangeArgs(0, 1),
		RunE: withTimeout(time.Hour, func(ctx context.Context, _ *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			rec, err := a.publishRelease(ctx, publishOptions{
				dir: dir, key: key, ref: ref, remote: remote, push: push, dryRun: dryRun, unsigned: unsigned, skipTests: skipTests,
			})
			if err != nil {
				return err
			}
			if out != "" {
				data, _ := json.MarshalIndent(rec, "", "  ")
				if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
					return err
				}
			}
			if submit {
				if dryRun {
					fmt.Fprintln(a.errOut, "hive: --dry-run: not submitting")
				} else {
					url, err := a.submitRelease(ctx, registryDir, rec)
					if err != nil {
						return fmt.Errorf("the release is tagged and recorded, but not submitted: %w", err)
					}
					fmt.Fprintf(a.errOut, "Opened %s\n", url)
				}
			}
			return a.emit(rec, func() error {
				data, _ := json.MarshalIndent(rec, "", "  ")
				_, err := fmt.Fprintln(a.out, string(data))
				return err
			})
		}),
	}
	cmd.Flags().StringVar(&key, "key", "", "SSH key to sign with: a private key file, or a public key file whose private key is in ssh-agent (default: git config user.signingkey)")
	cmd.Flags().StringVar(&ref, "ref", "", "the tag to make (default v<version>, or <id>/v<version> in a subdirectory)")
	cmd.Flags().StringVar(&out, "out", "", "write the release record to this file")
	cmd.Flags().StringVar(&remote, "git-remote", "origin", "the git remote to push the tag to")
	cmd.Flags().BoolVar(&push, "push", false, "push the tag")
	cmd.Flags().BoolVar(&submit, "submit", false, "append the release to the plugin's registry submission and open a pull request (needs --registry and gh)")
	cmd.Flags().StringVar(&registryDir, "registry", "", "a checkout of the registry repository, for --submit")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "check, test, checksum and sign, but do not tag, push or submit")
	cmd.Flags().BoolVar(&unsigned, "unsigned", false, "publish without a signature (community plugins only; official and verified releases must be signed)")
	cmd.Flags().BoolVar(&skipTests, "skip-tests", false, "do not run hive plugin test first")
	return cmd
}

type publishOptions struct {
	dir, key, ref, remote             string
	push, dryRun, unsigned, skipTests bool
	now                               func() time.Time
}

func (a *app) publishRelease(ctx context.Context, o publishOptions) (plugin.ReleaseRecord, error) {
	if o.now == nil {
		o.now = time.Now
	}
	dir, err := filepath.Abs(o.dir)
	if err != nil {
		return plugin.ReleaseRecord{}, err
	}
	m, err := plugin.LoadManifest(dir)
	if err != nil {
		return plugin.ReleaseRecord{}, err
	}
	if m.Legacy() {
		return plugin.ReleaseRecord{}, fmt.Errorf("%s declares no plugin API: add api = %d and [permissions] before publishing", m.ID, plugin.APIVersion)
	}
	if m.Test != nil && (m.MinHiveVersion == "" || update.Newer(testsSince, m.MinHiveVersion)) {
		return plugin.ReleaseRecord{}, fmt.Errorf("the manifest's [test] table needs min_hive_version = %q or later: earlier Hive releases reject the key", testsSince)
	}
	root, err := gitOutput(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return plugin.ReleaseRecord{}, fmt.Errorf("%s is not in a git repository: %w", dir, err)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	realDir := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		realDir = r
	}
	subdir, err := filepath.Rel(root, realDir)
	if err != nil || subdir == ".." || strings.HasPrefix(subdir, ".."+string(filepath.Separator)) {
		return plugin.ReleaseRecord{}, fmt.Errorf("%s is outside its repository", dir)
	}
	subdir = filepath.ToSlash(subdir)
	if subdir == "." {
		subdir = ""
	}
	status, err := gitOutput(ctx, root, "status", "--porcelain")
	if err != nil {
		return plugin.ReleaseRecord{}, err
	}
	if status != "" {
		return plugin.ReleaseRecord{}, fmt.Errorf("the repository has uncommitted changes; commit or stash them first:\n%s", status)
	}
	head, err := gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return plugin.ReleaseRecord{}, err
	}
	ref := o.ref
	if ref == "" {
		ref = "v" + m.Version
		if subdir != "" {
			ref = m.ID + "/v" + m.Version
		}
	}
	if !plugin.RefMatchesVersion(ref, m.Version) {
		return plugin.ReleaseRecord{}, fmt.Errorf("tag %s does not name version %s (want v%s, or a prefix ending in /v%s)", ref, m.Version, m.Version, m.Version)
	}
	tagged := false
	if at, err := gitOutput(ctx, root, "rev-parse", "-q", "--verify", "refs/tags/"+ref+"^{commit}"); err == nil && at != "" {
		if at != head {
			return plugin.ReleaseRecord{}, fmt.Errorf("tag %s already points at %s, not %s: releases are immutable; bump the version", ref, at[:12], head[:12])
		}
		tagged = true
	}
	if !o.skipTests {
		self, err := os.Executable()
		if err != nil {
			return plugin.ReleaseRecord{}, err
		}
		fmt.Fprintf(a.errOut, "Testing %s %s…\n", m.ID, m.Version)
		res := a.runPluginTests(ctx, dir, m, self, buildinfo.Get().Version)
		if res.Result != "pass" {
			return plugin.ReleaseRecord{}, fmt.Errorf("hive plugin test failed: %s", res.failure())
		}
	}
	release := plugin.Release{
		Version: m.Version, Ref: ref, Commit: head, Subdir: subdir, PluginAPI: m.EffectiveAPI(), MinHiveVersion: m.MinHiveVersion,
		PublishedAt: o.now().UTC().Truncate(time.Second), Provenance: plugin.ProvenanceFromEnv(a.getenv),
	}
	if release.Checksum, err = plugin.ArchiveChecksum(ctx, root, head); err != nil {
		return plugin.ReleaseRecord{}, err
	}
	if !o.unsigned {
		key, pub, cleanup, err := signingKey(ctx, root, o.key)
		if err != nil {
			return plugin.ReleaseRecord{}, err
		}
		defer cleanup()
		statement := plugin.ReleaseStatement(m.ID, release)
		sig, err := plugin.SignWithSSHKeygen(ctx, key, statement)
		if err != nil {
			return plugin.ReleaseRecord{}, err
		}
		if _, err := plugin.VerifySignature(sig, statement, []plugin.PublicKey{pub}); err != nil {
			return plugin.ReleaseRecord{}, fmt.Errorf("the signature ssh-keygen made does not verify: %w", err)
		}
		release.Signature, release.SignedBy = sig, pub.Fingerprint()
		fmt.Fprintf(a.errOut, "Signed with %s\n", pub.Fingerprint())
	}
	if !o.dryRun {
		if !tagged {
			if _, err := gitOutput(ctx, root, "tag", ref, head); err != nil {
				return plugin.ReleaseRecord{}, err
			}
			fmt.Fprintf(a.errOut, "Tagged %s at %s\n", ref, head[:12])
		}
		if o.push {
			if _, err := gitOutput(ctx, root, "push", o.remote, "refs/tags/"+ref); err != nil {
				return plugin.ReleaseRecord{}, err
			}
			fmt.Fprintf(a.errOut, "Pushed %s to %s\n", ref, o.remote)
		}
	}
	return plugin.ReleaseRecord{ID: m.ID, Release: release}, nil
}

// signingKey finds the SSH key to sign with: given, or git's
// user.signingkey (a path, or "key::<public key>" for ssh-agent). It
// returns the file ssh-keygen signs with, the public key, and what removes
// any file it wrote.
func signingKey(ctx context.Context, repo, given string) (string, plugin.PublicKey, func(), error) {
	none := func() {}
	key := given
	if key == "" {
		key, _ = gitOutput(ctx, repo, "config", "user.signingkey")
	}
	if key == "" {
		return "", plugin.PublicKey{}, none, errors.New("no signing key: pass --key, or set git config user.signingkey (community plugins may publish --unsigned)")
	}
	if literal, ok := strings.CutPrefix(key, "key::"); ok {
		pub, err := plugin.ParsePublicKey(literal)
		if err != nil {
			return "", plugin.PublicKey{}, none, err
		}
		f, err := os.CreateTemp("", "hive-signing-*.pub")
		if err != nil {
			return "", plugin.PublicKey{}, none, err
		}
		cleanup := func() { _ = os.Remove(f.Name()) }
		_, werr := f.WriteString(pub.String() + "\n")
		if err := errors.Join(werr, f.Close()); err != nil {
			cleanup()
			return "", plugin.PublicKey{}, none, err
		}
		return f.Name(), pub, cleanup, nil
	}
	if rest, ok := strings.CutPrefix(key, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", plugin.PublicKey{}, none, err
		}
		key = filepath.Join(home, rest)
	}
	pubFile := key
	if !strings.HasSuffix(key, ".pub") {
		pubFile = key + ".pub"
	}
	line, err := os.ReadFile(pubFile)
	if errors.Is(err, os.ErrNotExist) && pubFile != key {
		// No .pub beside the private key: derive it.
		out, kerr := exec.CommandContext(ctx, "ssh-keygen", "-y", "-f", key).Output()
		if kerr != nil {
			return "", plugin.PublicKey{}, none, fmt.Errorf("read the public half of %s: %w", key, kerr)
		}
		line, err = out, nil
	}
	if err != nil {
		return "", plugin.PublicKey{}, none, err
	}
	pub, err := plugin.ParsePublicKey(string(line))
	return key, pub, none, err
}

// submitRelease appends the release to the plugin's submission record in
// a checkout of the registry repository, and opens a pull request. The
// submission's state and history are left to the registry's reviewers.
func (a *app) submitRelease(ctx context.Context, registryDir string, rec plugin.ReleaseRecord) (string, error) {
	if registryDir == "" {
		return "", errors.New("--submit needs --registry, a checkout of the registry repository (github.com/admirable-oss/hive)")
	}
	path := filepath.Join(registryDir, "registry", "submissions", rec.ID+".json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%s has no submission yet: submit its listing first (docs/plugins/governance.md)", rec.ID)
	}
	if err != nil {
		return "", err
	}
	if status, err := gitOutput(ctx, registryDir, "status", "--porcelain"); err != nil {
		return "", err
	} else if status != "" {
		return "", fmt.Errorf("the registry checkout %s has uncommitted changes", registryDir)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	listing, ok := doc["listing"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("%s has no listing", path)
	}
	releases, _ := listing["releases"].([]any)
	for _, r := range releases {
		if m, ok := r.(map[string]any); ok && m["version"] == rec.Release.Version {
			return "", fmt.Errorf("%s %s is already submitted: releases are immutable", rec.ID, rec.Release.Version)
		}
	}
	entry, err := submissionEntry(rec.Release)
	if err != nil {
		return "", err
	}
	listing["releases"] = append(releases, entry)
	doc["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	branch := "plugin/" + rec.ID + "-" + rec.Release.Version
	if _, err := gitOutput(ctx, registryDir, "checkout", "-b", branch); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return "", err
	}
	title := fmt.Sprintf("plugin: release %s %s", rec.ID, rec.Release.Version)
	for _, args := range [][]string{{"add", path}, {"commit", "-m", title}, {"push", "-u", "origin", branch}} {
		if _, err := gitOutput(ctx, registryDir, args...); err != nil {
			return "", err
		}
	}
	signed := "unsigned"
	if rec.Release.SignedBy != "" {
		signed = "signed by " + rec.Release.SignedBy
	}
	body := fmt.Sprintf("Release %s of %s at %s (%s), %s, built by %s.\n\nMade with `hive plugin publish`.", rec.Release.Version, rec.ID, rec.Release.Commit, rec.Release.Ref, signed, rec.Release.Provenance)
	cmd := exec.CommandContext(ctx, "gh", "pr", "create", "--title", title, "--body", body)
	cmd.Dir = registryDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh pr create: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// submissionEntry is a release as a submission records it: the schema 2
// release, without what only the registry's readers decide.
func submissionEntry(r plugin.Release) (map[string]any, error) {
	r.SignedBy, r.SignatureError, r.Revoked = "", "", nil
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	var entry map[string]any
	err = json.Unmarshal(raw, &entry)
	return entry, err
}

func newPluginTestCmd(a *app) *cobra.Command {
	var hive string
	cmd := &cobra.Command{
		Use:   "test [dir-or-id]",
		Short: "Check a plugin against a Hive release: validate, install, doctor and its own [test] command",
		Long: `Test checks the plugin in dir (default: here), or the newest release of a
catalog plugin by ID, against a Hive: this one, a hive binary (--hive
./hive), or a release by version (--hive 1.7.0-rc.1), downloaded and checked
against its signed checksums. It runs, in a scratch HIVE_HOME:

  hive plugin validate, hive plugin link, hive plugin doctor, and the
  manifest's [test] command (with HIVE_BIN and HIVE_HOME set).

It prints the result (pass or fail, with each check) and exits 1 on a
failure. The registry's plugin-compat workflow runs it for every listed
plugin against each Hive release candidate.`,
		Example: `  hive plugin test
  hive plugin test --hive 1.7.0-rc.1
  hive plugin test pr-status --json`,
		Args: rangeArgs(0, 1),
		RunE: withTimeout(time.Hour, func(ctx context.Context, _ *cobra.Command, args []string) error {
			target := "."
			if len(args) == 1 {
				target = args[0]
			}
			bin, version, cleanup, err := a.hiveUnderTest(ctx, hive)
			if err != nil {
				return err
			}
			defer cleanup()
			dir := target
			if _, statErr := os.Stat(target); errors.Is(statErr, os.ErrNotExist) && isPluginID(target) {
				f, err := a.fetchCatalogRelease(ctx, target)
				if err != nil {
					return err
				}
				defer f.Discard()
				dir = f.Dir()
			}
			m, err := plugin.LoadManifest(dir)
			if err != nil {
				return err
			}
			res := a.runPluginTests(ctx, dir, m, bin, version)
			err = a.emit(res, func() error {
				fmt.Fprintf(a.out, "%s %s with Hive %s: %s\n", m.ID, m.Version, res.Hive, res.Result)
				for _, c := range res.Checks {
					mark := "✓"
					if !c.OK {
						mark = "✗"
					}
					fmt.Fprintf(a.out, "  %s %s%s\n", mark, c.Name, suffixIf(c.Detail, ": "+c.Detail))
				}
				return nil
			})
			if err == nil && res.Result != "pass" {
				return &codedError{code: exitError, msg: "plugin test failed"}
			}
			return err
		}),
	}
	cmd.Flags().StringVar(&hive, "hive", "", "the Hive to test against: a hive binary, or a release version to download (default: this hive)")
	return cmd
}

// pluginTestResult is what `hive plugin test` found.
type pluginTestResult struct {
	Plugin  string            `json:"plugin"`
	Version string            `json:"version"`
	Hive    string            `json:"hive"`
	Result  string            `json:"result"` // pass or fail
	At      time.Time         `json:"at"`
	Checks  []pluginTestCheck `json:"checks"`
}

type pluginTestCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

func (r pluginTestResult) failure() string {
	for _, c := range r.Checks {
		if !c.OK {
			return c.Name + suffixIf(c.Detail, ": "+c.Detail)
		}
	}
	return "unknown"
}

// runPluginTests checks the plugin in dir against the hive binary bin, in
// a scratch HIVE_HOME.
func (a *app) runPluginTests(ctx context.Context, dir string, m *plugin.Manifest, bin, version string) pluginTestResult {
	res := pluginTestResult{Plugin: m.ID, Version: m.Version, Hive: version, Result: "pass", At: time.Now().UTC().Truncate(time.Second)}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	check := func(name string, err error, detail string) bool {
		c := pluginTestCheck{Name: name, OK: err == nil}
		if err != nil {
			c.Detail = strings.TrimSpace(cmpStr(detail, err.Error()))
			if detail != "" {
				c.Detail = err.Error() + "\n" + strings.TrimSpace(detail)
			}
			res.Result = "fail"
		}
		res.Checks = append(res.Checks, c)
		return err == nil
	}
	// Short: the daemon socket under it must fit macOS's 104-byte limit.
	home, err := os.MkdirTemp("", "hpt")
	if !check("scratch home", err, "") {
		return res
	}
	defer os.RemoveAll(home)
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return k == "HIVE_HOME" || k == "HIVE_CONFIG" || k == "HIVE_SESSION" || k == "HIVE_SOCKET"
	})
	env = append(env, "HIVE_HOME="+home, "HIVE_CONFIG="+filepath.Join(home, "config.toml"), "HIVE_BIN="+bin, "HIVE_PLUGIN_TEST=1", "HIVE_PLUGIN_TEST_HIVE="+version)
	run := func(timeout time.Duration, wd string, argv ...string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir, cmd.Env = wd, env
		cmd.WaitDelay = 5 * time.Second
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s", timeout)
		}
		return lastLines(out.String(), 20), err
	}
	// A daemon the checks may start is stopped with the scratch home.
	defer func() { _, _ = run(30*time.Second, dir, bin, "stop") }()
	out, err := run(2*time.Minute, dir, bin, "plugin", "validate", dir)
	if !check("validate", err, out) {
		return res
	}
	out, err = run(2*time.Minute, dir, bin, "--yes", "plugin", "link", dir)
	if !check("install", err, out) {
		return res
	}
	out, err = run(2*time.Minute, dir, bin, "plugin", "doctor", m.ID)
	check("doctor", err, out)
	if m.Test != nil {
		argv := slices.Clone(m.Test.Command)
		if !filepath.IsAbs(argv[0]) && strings.ContainsRune(argv[0], '/') {
			argv[0] = filepath.Join(dir, argv[0])
		}
		out, err = run(m.Test.TestTimeout(), dir, argv...)
		check("test", err, out)
	}
	return res
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// hiveUnderTest resolves --hive: this hive, a binary, or a release version
// downloaded and verified as `hive update` does.
func (a *app) hiveUnderTest(ctx context.Context, hive string) (bin, version string, cleanup func(), err error) {
	cleanup = func() {}
	if hive == "" {
		self, err := os.Executable()
		return self, buildinfo.Get().Version, cleanup, err
	}
	if info, err := os.Stat(hive); err == nil && !info.IsDir() {
		abs, err := filepath.Abs(hive)
		if err != nil {
			return "", "", cleanup, err
		}
		out, verr := exec.CommandContext(ctx, abs, "version", "--json").Output()
		var v struct {
			Version string `json:"version"`
		}
		if verr != nil || json.Unmarshal(out, &v) != nil || v.Version == "" {
			return "", "", cleanup, fmt.Errorf("%s is not a hive binary (hive version --json failed)", hive)
		}
		return abs, v.Version, cleanup, nil
	}
	src := update.Source{}
	if k := buildinfo.ReleaseKey(); k != "" {
		key, err := update.ParseKey(k)
		if err != nil {
			return "", "", cleanup, err
		}
		src.PublicKey = key
	}
	fmt.Fprintf(a.errOut, "Downloading Hive %s…\n", hive)
	data, err := src.Fetch(ctx, update.ReleaseTag(hive), runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", "", cleanup, fmt.Errorf("download Hive %s: %w", hive, err)
	}
	dir, err := os.MkdirTemp("", "hive-under-test-")
	if err != nil {
		return "", "", cleanup, err
	}
	name := "hive"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin = filepath.Join(dir, name)
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", cleanup, err
	}
	return bin, strings.TrimPrefix(hive, "v"), func() { _ = os.RemoveAll(dir) }, nil
}

// fetchCatalogRelease fetches the newest installable release of a catalog
// plugin, verified as an install would be, without installing it.
func (a *app) fetchCatalogRelease(ctx context.Context, id string) (*plugin.Fetched, error) {
	catalog, err := a.pluginRegistry().Fetch(ctx)
	if err != nil {
		return nil, err
	}
	entry, ok := catalog.Get(id)
	if !ok {
		return nil, fmt.Errorf("plugin %q is not in the registry", id)
	}
	release, ok := entry.LatestFor(buildinfo.Get().Version)
	if !ok {
		return nil, fmt.Errorf("no release of %s can be installed", id)
	}
	if err := a.checkReleaseSignature(catalog, entry, release); err != nil {
		return nil, err
	}
	f, err := a.plugins().FetchSubdir(ctx, entry.Repository, release.Ref, release.Subdir)
	if err != nil {
		return nil, err
	}
	if err := f.VerifyArchiveChecksum(ctx, release.Checksum); err != nil {
		f.Discard()
		return nil, err
	}
	if f.Manifest.ID != id || !strings.EqualFold(f.Commit, release.Commit) {
		f.Discard()
		return nil, fmt.Errorf("registry release %s@%s does not match its manifest or pinned source revision", id, release.Version)
	}
	return f, nil
}

// gitOutput runs git in dir and returns its trimmed output.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return strings.TrimSpace(out.String()), nil
}
