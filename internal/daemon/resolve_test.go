package daemon

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestResolveAnswersTheUnitAPluginReadsAnAddressAs(t *testing.T) {
	t.Parallel()
	seg := unit.Unit{Provider: "slack", Key: "T000/D000@1"}
	s := newServerWith(t, func(d *Deps) {
		d.Resolve = func(_ context.Context, address string) (unit.Unit, bool, error) {
			switch address {
			case "D000/1700000000.000100":
				return seg, true, nil
			case "C000/1700000000.000100":
				return unit.Unit{}, false, errors.New("not_authed")
			}
			return unit.Unit{}, false, nil
		}
	})
	path := func(address string) string { return api.Resolve + "?" + url.Values{"address": {address}}.Encode() }
	var got api.Resolved
	if code := get(t, s, path("D000/1700000000.000100"), &got); code != 200 || !got.Found || got.Unit != seg {
		t.Fatalf("a DM ref: %d %+v", code, got)
	}
	got = api.Resolved{}
	if code := get(t, s, path("acme/api#7"), &got); code != 200 || got.Found {
		t.Fatalf("an address no plugin reads: %d %+v", code, got)
	}
	if code := get(t, s, path("C000/1700000000.000100"), nil); code != 502 {
		t.Fatalf("a plugin's error: %d", code)
	}
	if code := get(t, s, api.Resolve, nil); code != 400 {
		t.Fatalf("no address: %d", code)
	}
}

func TestResolveWithoutResolversFindsNothing(t *testing.T) {
	t.Parallel()
	var got api.Resolved
	if code := get(t, newServer(t), api.Resolve+"?address=D000%2F1700000000.000100", &got); code != 200 || got.Found {
		t.Fatalf("%d %+v", code, got)
	}
}
