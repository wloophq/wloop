package store

import (
	"context"
	"testing"
)

func TestMetaPrefixed(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	for k, v := range map[string]string{"git_access/github.com": "a", "git_access/gitlab.com": "b", "git_accessory": "c", "other": "d"} {
		if err := db.SetMeta(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.MetaPrefixed(ctx, "git_access/")
	if err != nil || len(got) != 2 || got["git_access/github.com"] != "a" || got["git_access/gitlab.com"] != "b" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestSetMetasWritesEveryKeyAndKeepsTheViewer(t *testing.T) {
	t.Parallel()
	db := open(t)
	ctx := context.Background()
	if err := db.SetMetas(ctx, map[string]string{"fast_sweep_at": "a", "slow_sweep_at": "b"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := db.Meta(ctx, "slow_sweep_at"); v != "b" {
		t.Fatalf("slow_sweep_at %q", v)
	}
	if v, err := db.Viewer(ctx); err != nil || v != "" {
		t.Fatalf("viewer before it is known %q %v", v, err)
	}
	if err := db.SetViewer(ctx, "ana"); err != nil {
		t.Fatal(err)
	}
	if v, err := db.Viewer(ctx); err != nil || v != "ana" {
		t.Fatalf("viewer %q %v", v, err)
	}
	if v, _ := db.Meta(ctx, ViewerKey); v != "ana" {
		t.Fatalf("the viewer is not under %s: %q", ViewerKey, v)
	}
}
