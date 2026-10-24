package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

func TestMajor(t *testing.T) {
	for in, want := range map[string]string{"1.0": "1", "1.7": "1", "2.0": "2", "": "0", " 3 ": "3", "10.2.1": "10"} {
		if got := Major(in); got != want {
			t.Errorf("Major(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionHandle(t *testing.T) {
	for _, ok := range []string{"orca:term-1", "a", "A.b_c-9:x", strings.Repeat("x", 128)} {
		if got, err := SessionHandle(ok); err != nil || got != ok {
			t.Errorf("SessionHandle(%q) = %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{"", "${ORCA_TERMINAL_HANDLE}", "a b", "a/b", strings.Repeat("x", 129), "x\u202e"} {
		if _, err := SessionHandle(bad); !errors.Is(err, ErrSessionHandle) {
			t.Errorf("SessionHandle(%q) took it: %v", bad, err)
		}
	}
	if !strings.HasPrefix(ErrSessionHandle.Error(), SessionHeader+" ") {
		t.Fatalf("the refusal does not name the header first: %s", ErrSessionHandle)
	}
}

func TestSessionHandleRefusalNamesNoManager(t *testing.T) {
	want := SessionHeader + " is missing or not a process handle: this session does not run under one of " + brand.Name + "'s workspace managers, so " + brand.Name + " cannot tell which turn it records"
	if got := ErrSessionHandle.Error(); got != want {
		t.Fatalf("%s", got)
	}
}

func TestMergeKeepsEveryNotice(t *testing.T) {
	c := Change{Type: "changed", Epoch: "e", Rev: 2}
	c.Merge(Change{Type: "notify", Epoch: "e", Rev: 2, Notices: []Notice{{ID: "a"}}})
	c.Merge(Change{Type: "notify", Epoch: "e", Rev: 2, Notices: []Notice{{ID: "b"}}})
	if len(c.Notices) != 2 || c.Notices[0].ID != "a" || c.Notices[1].ID != "b" || c.All {
		t.Fatalf("merged %+v", c)
	}
}

func TestMergeKeepsTheNewestCounters(t *testing.T) {
	c := Change{Type: "changed", Epoch: "e", Rev: 2}
	c.Merge(Change{Type: "changed", Epoch: "e", Rev: 2, Counters: &Counters{Badge: 1}})
	c.Merge(Change{Type: "changed", Epoch: "e", Rev: 3})
	if c.Counters == nil || c.Counters.Badge != 1 {
		t.Fatalf("a later change without counters dropped them: %+v", c.Counters)
	}
	c.Merge(Change{Type: "changed", Epoch: "e", Rev: 3, Counters: &Counters{Badge: 2}})
	if c.Counters.Badge != 2 {
		t.Fatalf("kept older counters: %+v", c.Counters)
	}
}
