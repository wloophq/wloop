package eval

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wloophq/wloop/plugins/slack/extract"
)

// profileDir is the plugin's profile layer.
const profileDir = "../profile"

var shipped = sync.OnceValues(func() (*extract.Prompts, error) {
	tmpl, err := os.ReadFile(filepath.Join(profileDir, extract.PromptFile))
	if err != nil {
		return nil, err
	}
	brief, err := os.ReadFile(filepath.Join(profileDir, extract.BriefSchemaFile))
	if err != nil {
		return nil, err
	}
	return extract.NewPrompts(string(tmpl), string(brief))
})

func shippedPrompts(t testing.TB) *extract.Prompts {
	t.Helper()
	p, err := shipped()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func extractionSchema() string {
	p, err := shipped()
	if err != nil {
		panic(err)
	}
	return p.Schema()
}
