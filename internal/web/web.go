// Package web serves the cockpit's static files.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"maps"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/brand"
)

//go:generate go run ../designtokens/cmd/gencss ../../design/tokens.json static/app.css ../../design/components/preview.css ../../design/components/bundle.css

//go:embed static
var files embed.FS

func render(raw []byte) []byte {
	return []byte(strings.NewReplacer("{{name}}", brand.Name, "{{command}}", brand.Command).Replace(string(raw)))
}

// asset is a static file as served: its body and the validator of that body.
type asset struct {
	body []byte
	etag string
}

var assets = sync.OnceValue(func() map[string]asset {
	sub, _ := fs.Sub(files, "static")
	out := map[string]asset{}
	_ = fs.WalkDir(sub, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(sub, name)
		if err != nil {
			return err
		}
		if ext := path.Ext(name); ext == ".js" || ext == ".html" {
			raw = render(raw)
		}
		sum := sha256.Sum256(raw)
		out[name] = asset{body: raw, etag: `"` + hex.EncodeToString(sum[:16]) + `"`}
		return nil
	})
	return out
})

// Build names the cockpit a binary of version serves: it changes with the version and with any embedded
// static file, so a page can tell a daemon that serves other files from the one it loaded from.
func Build(version string) string { return buildOf(version, assets()) }

func buildOf(version string, all map[string]asset) string {
	h := sha256.New()
	h.Write([]byte(version))
	for _, name := range slices.Sorted(maps.Keys(all)) {
		h.Write([]byte{0})
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write([]byte(all[name].etag))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// serve answers with a, revalidated on every use so a reload never runs a cached file of another build.
func serve(w http.ResponseWriter, r *http.Request, name string, a asset) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", a.etag)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(a.body))
}

// csp allows only the cockpit's own scripts and no framing; styles stay
// open for the inline styles the charts write.
const csp = "script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"

// Handler serves the cockpit page of version at "/" and its assets under "/static/". The page names its
// Build and loads each asset under a URL of that build.
func Handler(version string) http.Handler {
	build := Build(version)
	index := assets()["index.html"]
	page := bytes.ReplaceAll(index.body, []byte("{{build}}"), []byte(build))
	sum := sha256.Sum256(page)
	index = asset{body: page, etag: `"` + hex.EncodeToString(sum[:16]) + `"`}
	mux := http.NewServeMux()
	mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/static/")
		a, ok := assets()[name]
		if !ok || name == "index.html" {
			http.NotFound(w, r)
			return
		}
		serve(w, r, name, a)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		serve(w, r, "index.html", index)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})
}
