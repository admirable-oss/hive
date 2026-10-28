package web

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/admirable-oss/hive/internal/observer"
)

func TestPluginsSection(t *testing.T) {
	c := newConn(Runtime{Name: "laptop"}, slog.New(slog.DiscardHandler))
	c.plugins = pluginsRead{at: time.Now(), ok: true, plugins: []observer.Plugin{
		{ID: "auto-tab-title", Name: "Automatic tab titles", Version: "0.2.0", Tier: "official", Publisher: "hive", SignedBy: "SHA256:off1", Sandbox: "sandbox-exec", Enabled: true, State: "ready"},
		{ID: "pr-status", Name: "PR status", Version: "0.1.0", Tier: "community", Sandbox: "none", Enabled: true, State: "ready", Revoked: &observer.PluginRevocation{Reason: "leaks the token", Advisory: "https://example.org/a/1"}},
	}}
	r := &shown{rt: Runtime{Name: "laptop"}, v: RuntimeView{loaded: true}, c: c}

	all := &page{grant: Grant{Scope: observer.Scope{All: true}}, now: time.Now()}
	html := renderString(pluginsSection(all, r))
	for _, want := range []string{"Plugins", "Automatic tab titles", "tier-official", "maintained by Hive&#39;s maintainers", "SHA256:off1", "sandbox-exec", `class="revoked"`, "Revoked: leaks the token", "https://example.org/a/1"} {
		if !strings.Contains(html, want) {
			t.Errorf("the section lacks %q:\n%s", want, html)
		}
	}
	scoped := &page{grant: Grant{Scope: observer.Scope{Environments: []string{"dev"}}}, now: time.Now()}
	if html := renderString(pluginsSection(scoped, r)); html != "" {
		t.Fatalf("a link scoped to part of the runtime sees no plugins: %s", html)
	}
}
