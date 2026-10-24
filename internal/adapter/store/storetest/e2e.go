package storetest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/pr"
)

// Event is a stored event row of an inventory database.
type Event struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
	Data string `json:"data"`
}

// Events reads the events of a pull request from the inventory database at
// path with the sqlite3 command on a read-only connection, oldest first.
func Events(ctx context.Context, t *testing.T, path string, ref pr.Ref) []Event {
	t.Helper()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	u := ref.Unit()
	q := fmt.Sprintf("SELECT kind, key, data FROM events WHERE provider = %s AND unit_key = %s ORDER BY at", quote(string(u.Provider)), quote(u.Key))
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "sqlite3", "-readonly", "-json", "file:"+path+"?mode=ro", q)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("events of %s: %v: %s", ref, err, strings.TrimSpace(stderr.String()))
	}
	var evs []Event
	if len(bytes.TrimSpace(stdout.Bytes())) == 0 {
		return evs
	}
	if err := json.Unmarshal(stdout.Bytes(), &evs); err != nil {
		t.Fatalf("events of %s: %v\n%.2000s", ref, err, stdout.Bytes())
	}
	return evs
}
