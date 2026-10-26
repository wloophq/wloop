package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func profileWithPartial(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "partials"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "partials", name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestProfileCheckFailsARecordPartialMissingTheProtocol(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	dir := profileWithPartial(t, "record.md.tmpl", "{{define \"record\"}}\n### Done\n\nTell the user you are done.\n{{end}}")
	if code := te.run("profile", "check", dir); code != 1 || !strings.Contains(te.err.String(), filepath.Join(dir, "partials", "record.md.tmpl")) ||
		!strings.Contains(te.err.String(), `"turn_end"`) {
		t.Fatalf("code %d\n%s", code, te.err.String())
	}
}

func TestProfileCheckPassesARecordPartialKeepingTheProtocol(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	dir := profileWithPartial(t, "record.md.tmpl", "{{define \"record\"}}\n### Done\n\nCall `turn_end`, or run `{{.Command}} turn end {{.Payload.Arg}} --turn <id>`, with `state` and `summary`.\n{{end}}")
	if code := te.run("profile", "check", dir); code != 0 {
		t.Fatalf("code %d\n%s", code, te.err.String())
	}
}
