package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// The browser's code and styles are embedded in hive: the web observer
// loads nothing from anywhere else (the Content-Security-Policy says so).
//
//go:embed assets
var assetFS embed.FS

// assetVersions holds a short content hash per asset, for cache busting.
var assetVersions = func() map[string]string {
	out := map[string]string{}
	_ = fs.WalkDir(assetFS, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := assetFS.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[strings.TrimPrefix(p, "assets/")] = hex.EncodeToString(sum[:6])
		return nil
	})
	return out
}()

// assetPath is where the page loads asset name from.
func assetPath(name string) string {
	return "/assets/" + name + "?v=" + assetVersions[name]
}

// serveAsset serves an embedded asset; versioned URLs are cached for good.
func serveAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/assets/")
	data, err := assetFS.ReadFile("assets/" + name)
	if err != nil || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if r.URL.Query().Get("v") == assetVersions[name] {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	_, _ = w.Write(data) //nolint:gosec // an asset embedded in hive, not user input
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}
