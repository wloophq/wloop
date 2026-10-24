package echo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/agent/echo"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/record"
)

type call struct{ kind, handle, id, line string }

func TestTypedPromptsStartTheTurnAndRecordTheScript(t *testing.T) {
	var calls []call
	a := &echo.Agent{
		Start: func(_ context.Context, handle, id string) error {
			calls = append(calls, call{"start", handle, id, ""})
			return nil
		},
		Record: func(_ context.Context, handle, id string, r record.Record) error {
			calls = append(calls, call{"record", handle, id, r.Summary})
			return nil
		},
		Reply: func(id, line string) (record.Record, bool) {
			return record.Record{State: record.Done, Summary: line}, !strings.HasPrefix(line, "quiet")
		},
	}
	ctx := context.Background()
	for _, line := range []string{"/" + brand.Name + "-review https://github.com/acme/api/pull/7 --turn abc", "yes, publish", "quiet"} {
		if ok, err := a.Type(ctx, "term_1", line); err != nil || !ok {
			t.Fatalf("Type(%q) = %v, %v", line, ok, err)
		}
	}
	want := []call{
		{"start", "term_1", "abc", ""},
		{"record", "term_1", "abc", "/" + brand.Name + "-review https://github.com/acme/api/pull/7 --turn abc"},
		{"record", "term_1", "abc", "yes, publish"},
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %+v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("call %d = %+v, want %+v", i, calls[i], want[i])
		}
	}
	if _, err := a.Type(ctx, "term_2", "hello"); err == nil {
		t.Fatal("a session with no turn accepted a line")
	}
	if a.Name() != "echo" {
		t.Fatal(a.Name())
	}
}

func TestEchoKindBinaryAndResume(t *testing.T) {
	a := &echo.Agent{}
	if a.Binary() != "cat" || a.Resume("/h", "x") != "cat" {
		t.Fatalf("Binary %q Resume %q", a.Binary(), a.Resume("/h", "x"))
	}
}
