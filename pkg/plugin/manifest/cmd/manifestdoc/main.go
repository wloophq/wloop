// Command manifestdoc writes plugin.toml's JSON schema and reference page
// from package manifest; make docs-ref runs it from the repository root.
package main

import (
	"log"
	"os"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

func main() {
	schema, err := manifest.Schema()
	if err != nil {
		log.Fatal(err)
	}
	for path, body := range map[string][]byte{
		"pkg/plugin/manifest/plugin.schema.json":             schema,
		"docs/src/content/docs/reference/plugin-manifest.md": []byte(manifest.Reference()),
	} {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
