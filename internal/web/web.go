// Package web serves the cockpit's static files.
package web

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/brand"
)

//go:embed static
var files embed.FS

func render(raw []byte) []byte {
	return []byte(strings.NewReplacer("{{name}}", brand.Name, "{{command}}", brand.Command).Replace(string(raw)))
}

func serve(sub fs.FS, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := fs.ReadFile(sub, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(render(raw)))
	}
}

// csp allows only the cockpit's own scripts and no framing; styles stay
// open for the inline styles the charts write.
const csp = "script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"

// Handler serves the cockpit page at "/" and its assets under "/static/".
func Handler() http.Handler {
	sub, _ := fs.Sub(files, "static")
	mux := http.NewServeMux()
	static := http.StripPrefix("/static/", http.FileServerFS(sub))
	mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		if name := strings.TrimPrefix(r.URL.Path, "/static/"); path.Ext(name) == ".js" {
			serve(sub, name)(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		serve(sub, "index.html")(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})
}
