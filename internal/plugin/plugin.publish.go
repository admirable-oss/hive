package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
)

// ReleaseRecord is a release as `hive plugin publish` writes it, for the
// registry: the plugin it belongs to and the schema 2 release entry.
type ReleaseRecord struct {
	ID      string  `json:"id"`
	Release Release `json:"release"`
}

// ArchiveChecksum is the SHA-256 of `git archive --format=tar <commit>` in
// repo, as the registry pins and every install checks.
func ArchiveChecksum(ctx context.Context, repo, commit string) (string, error) {
	h := sha256.New()
	w := &boundedHashWriter{h: h, limit: maxPluginArchiveBytes}
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "archive", "--format=tar", commit)
	cmd.Stdout = w
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if w.tooLarge {
			return "", fmt.Errorf("plugin source archive exceeds %d MiB", maxPluginArchiveBytes>>20)
		}
		return "", fmt.Errorf("git archive: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// ProvenanceFromEnv is how a release is being built: in GitHub Actions,
// from the run's environment; anywhere else, "local".
func ProvenanceFromEnv(getenv func(string) string) *Provenance {
	if getenv("GITHUB_ACTIONS") != "true" {
		return &Provenance{Builder: "local"}
	}
	server := strings.TrimSuffix(getenv("GITHUB_SERVER_URL"), "/")
	if server == "" {
		server = "https://github.com"
	}
	repo := getenv("GITHUB_REPOSITORY")
	p := &Provenance{Builder: "github-actions"}
	if repo != "" {
		p.Repository = server + "/" + repo
		if run := getenv("GITHUB_RUN_ID"); run != "" {
			p.Run = p.Repository + "/actions/runs/" + run
		}
	}
	// GITHUB_WORKFLOW_REF is owner/repo/.github/workflows/x.yml@refs/...
	if ref := getenv("GITHUB_WORKFLOW_REF"); ref != "" {
		path, _, _ := strings.Cut(ref, "@")
		if repo != "" {
			path = strings.TrimPrefix(path, repo+"/")
		}
		p.Workflow = path
	}
	if !isHTTPSURL(p.Repository) {
		p.Repository, p.Run = "", ""
	}
	return p
}

// SignWithSSHKeygen signs a release statement with `ssh-keygen -Y sign`:
// key is a private key file, or a public key file whose private half is in
// ssh-agent.
func SignWithSSHKeygen(ctx context.Context, key string, statement []byte) (string, error) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		return "", fmt.Errorf("signing needs ssh-keygen (OpenSSH 8.0 or later): %w", err)
	}
	cmd := exec.CommandContext(ctx, keygen, "-Y", "sign", "-n", SignatureNamespace, "-f", key)
	cmd.Stdin = bytes.NewReader(statement)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ssh-keygen -Y sign: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return out.String(), nil
}
