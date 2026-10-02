// Package docs serves the generated AsyncAPI documentation bundled in the binary.
package docs

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed asyncapi asyncapi.yaml
var asyncAPI embed.FS

// AsyncAPIHandler serves generated assets with no runtime filesystem dependency.
// Mount it with /asyncapi stripped; directory listings and build metadata are hidden.
func AsyncAPIHandler() http.Handler {
	root, err := fs.Sub(asyncAPI, "asyncapi")
	if err != nil {
		panic("docs: invalid embedded AsyncAPI root")
	}
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		info, err := fs.Stat(root, name)
		if err != nil || info.IsDir() || name == "source.sha256" {
			http.NotFound(w, r)
			return
		}
		// Support the Swagger-style index.html URL without FileServer's redirect.
		if name == "index.html" {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	})
}
