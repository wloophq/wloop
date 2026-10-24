//go:build darwin

package macos

import (
	"strings"
	"testing"

	"github.com/ebitengine/purego/objc"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/menubar"
)

var (
	_ menubar.Tray     = (*tray)(nil)
	_ menubar.Notifier = (*notifier)(nil)
)

func TestPermissionNames(t *testing.T) {
	cases := map[int]menubar.Permission{0: menubar.PermissionNotDetermined, 1: menubar.PermissionDenied, 2: menubar.PermissionAuthorized, 3: menubar.PermissionProvisional, 4: menubar.PermissionAuthorized}
	for status, want := range cases {
		if got := permission(status); got != want {
			t.Errorf("permission(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestStringsRoundTrip(t *testing.T) {
	loadFrameworks()
	for _, s := range []string{"", "acme/api #7", "Round in 60 s · ação"} {
		if got := goString(nsString(s)); got != s {
			t.Errorf("round trip %q -> %q", s, got)
		}
	}
}

func TestMenuTagsMapToActions(t *testing.T) {
	loadFrameworks()
	setupPump()
	called := make(chan string, 2)
	resetActions()
	a := tagFor(func() { called <- "a" })
	b := tagFor(func() { called <- "b" })
	runTag(b)
	runTag(a)
	if got := <-called + <-called; got != "ba" && got != "ab" {
		t.Fatalf("%q", got)
	}
	resetActions()
	resetActions()
	runTag(a)
	select {
	case got := <-called:
		t.Fatalf("a tag survived two resets: %q", got)
	default:
	}
}

func TestAStaleClickRunsItsOwnAction(t *testing.T) {
	loadFrameworks()
	setupPump()
	called := make(chan string, 1)
	resetActions()
	old := tagFor(func() { called <- "old" })
	resetActions()
	fresh := tagFor(func() { called <- "new" })
	if fresh == old {
		t.Fatalf("tag %d reused", old)
	}
	runTag(old)
	if got := <-called; got != "old" {
		t.Fatalf("the old menu's click ran %q", got)
	}
}

func TestCallBlockReachesASystemStyleBlock(t *testing.T) {
	got := make(chan uintptr, 1)
	b := objc.NewBlock(func(_ objc.Block, x uintptr) { got <- x })
	defer b.Release()
	callBlock(b, 42)
	if x := <-got; x != 42 {
		t.Fatalf("got %d", x)
	}
}

func TestNSErrorCarriesTheDescription(t *testing.T) {
	loadFrameworks()
	if err := nsError(0); err != nil {
		t.Fatalf("nil NSError became %v", err)
	}
	e := class("NSError").Send(sel("errorWithDomain:code:userInfo:"), nsString(brand.Name), 7, objc.ID(0))
	if err := nsError(e); err == nil || !strings.Contains(err.Error(), brand.Name) {
		t.Fatalf("%v", err)
	}
}
