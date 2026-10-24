package placement_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wloophq/wloop/internal/core/placement"
	dplacement "github.com/wloophq/wloop/internal/domain/placement"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type overrides map[unit.Unit]dplacement.Placement

func (o overrides) Override(_ context.Context, u unit.Unit) (dplacement.Placement, bool, error) {
	p, ok := o[u]
	return p, ok, nil
}

func (o overrides) SetOverride(_ context.Context, u unit.Unit, p dplacement.Placement) error {
	if p == (dplacement.Placement{}) {
		delete(o, u)
		return nil
	}
	o[u] = p
	return nil
}

type fixed struct {
	p  dplacement.Placement
	ok bool
}

func (f fixed) Suggested(context.Context, unit.Unit, string) (dplacement.Placement, bool, error) {
	return f.p, f.ok, nil
}

func (f fixed) Default(context.Context, unit.Unit, string) (dplacement.Placement, bool, error) {
	return f.p, f.ok, nil
}

const thread unit.ProviderID = "slack"

var (
	threadA = unit.Unit{Provider: thread, Key: "T000/C001/1700000000.1"}
	threadB = unit.Unit{Provider: thread, Key: "T000/C002/1700000000.2"}
)

func service(rules []dplacement.Rule, o overrides, hint, def *fixed) *placement.Service {
	s := &placement.Service{Map: func() dplacement.Map { return dplacement.Map{Rules: rules} }, Overrides: o}
	if hint != nil {
		s.Hints = map[unit.ProviderID]dplacement.Hints{thread: *hint}
	}
	if def != nil {
		s.Defaults = map[unit.ProviderID]dplacement.Defaults{thread: *def}
	}
	return s
}

func TestOverrideWins(t *testing.T) {
	o := overrides{threadA: {Repo: "acme/over", Role: "work"}}
	s := service([]dplacement.Rule{{Match: "slack:*", Repo: "acme/rule"}}, o,
		&fixed{dplacement.Placement{Repo: "acme/hint"}, true}, &fixed{dplacement.Placement{Repo: "acme/def"}, true})
	got, err := s.Place(context.Background(), threadA, "")
	if err != nil || got.Repo != "acme/over" || got.Source != "override" {
		t.Fatalf("Place = %+v %v", got, err)
	}
}

func TestPlaceHintOrder(t *testing.T) {
	hint, def := &fixed{dplacement.Placement{Repo: "acme/hint"}, true}, &fixed{dplacement.Placement{Repo: "acme/def"}, true}
	s := service([]dplacement.Rule{{Match: "slack:T000/C001/*", Repo: "acme/rule", Shared: true}}, overrides{}, hint, def)
	got, err := s.Place(context.Background(), threadA, "")
	if err != nil || got.Repo != "acme/rule" || got.Source != "context" || !got.Shared {
		t.Fatalf("rule over hint: %+v %v", got, err)
	}
	got, err = s.Place(context.Background(), threadB, "")
	if err != nil || got.Repo != "acme/hint" || got.Source != "suggested" {
		t.Fatalf("hint over default: %+v %v", got, err)
	}
	s = service(nil, overrides{}, &fixed{}, def)
	got, err = s.Place(context.Background(), threadB, "")
	if err != nil || got.Repo != "acme/def" || got.Source != "provider" {
		t.Fatalf("default: %+v %v", got, err)
	}
}

func TestFirstMatchingRuleWins(t *testing.T) {
	s := service([]dplacement.Rule{{Match: "slack:T000/C001/*", Repo: "acme/first"}, {Match: "slack:*", Repo: "acme/second"}}, overrides{}, nil, nil)
	for u, want := range map[unit.Unit]string{threadA: "acme/first", threadB: "acme/second"} {
		got, err := s.Place(context.Background(), u, "")
		if err != nil || got.Repo != want {
			t.Errorf("%v: %+v %v, want %s", u, got, err, want)
		}
	}
	q := service([]dplacement.Rule{{Match: "slack:T000/C00?/1700000000.?", Repo: "acme/q"}}, overrides{}, nil, nil)
	if got, err := q.Place(context.Background(), threadB, ""); err != nil || got.Repo != "acme/q" {
		t.Errorf("? glob: %+v %v", got, err)
	}
}

func TestUnplacedUnitIsAnError(t *testing.T) {
	s := service([]dplacement.Rule{{Match: "github:*", Repo: "acme/x"}}, overrides{}, &fixed{}, &fixed{})
	if _, err := s.Place(context.Background(), threadA, ""); !errors.Is(err, placement.ErrUnplaced) {
		t.Fatalf("err = %v", err)
	}
	if _, err := (&placement.Service{}).Place(context.Background(), threadA, ""); !errors.Is(err, placement.ErrUnplaced) {
		t.Fatalf("an empty service: %v", err)
	}
}

func TestZeroOverrideClears(t *testing.T) {
	o := overrides{}
	s := service(nil, o, nil, &fixed{dplacement.Placement{Repo: "acme/def"}, true})
	ctx := context.Background()
	if err := o.SetOverride(ctx, threadA, dplacement.Placement{Repo: "acme/over"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Place(ctx, threadA, ""); got.Source != "override" {
		t.Fatalf("set: %+v", got)
	}
	if err := o.SetOverride(ctx, threadA, dplacement.Placement{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Place(ctx, threadA, ""); got.Source != "provider" {
		t.Fatalf("cleared: %+v", got)
	}
}

func TestRuleValid(t *testing.T) {
	for _, r := range []dplacement.Rule{{Match: ""}, {Match: "slack:[a]"}, {Match: "x", Checkout: "rel/path"}, {Match: "x", Role: "boss"}} {
		if r.Valid() == nil {
			t.Errorf("%+v is valid", r)
		}
	}
	for _, r := range []dplacement.Rule{{Match: "*"}, {Match: "slack:*", Checkout: "main"}, {Match: "github:a/b#?", Checkout: "/abs", Role: "work"}} {
		if err := r.Valid(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
}

func TestFixedCheckoutIsShared(t *testing.T) {
	ctx := context.Background()
	s := service([]dplacement.Rule{{Match: "slack:T000/C001/*", Repo: "acme/api", Checkout: "main"}}, overrides{threadB: {Repo: "acme/api", Checkout: "/abs/wt"}}, nil,
		&fixed{dplacement.Placement{Repo: "acme/api"}, true})
	for _, u := range []unit.Unit{threadA, threadB} {
		if got, err := s.Place(ctx, u, ""); err != nil || !got.Shared {
			t.Errorf("%v: %+v %v, want shared", u, got, err)
		}
	}
	if got, err := s.Place(ctx, unit.Unit{Provider: thread, Key: "T000/C009/1.1"}, ""); err != nil || got.Shared {
		t.Errorf("a new worktree: %+v %v, want not shared", got, err)
	}
}
