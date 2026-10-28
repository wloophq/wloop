package codec_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/pr"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/plugins/bundled"
)

// TestTheManifestCodecsReadKeysAsTheGoCodecsDid compares each bundled
// manifest's codec with the Go codec it replaces, key by key, and the label
// with the one Unit.Label gave before it asked the codec.
func TestTheManifestCodecsReadKeysAsTheGoCodecsDid(t *testing.T) {
	cs, err := bundled.Codecs()
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"acme/api#12", "acme/api#0", "acme/api#012", "acme/api", "acme#1", "/api#1", "acme/api#1x", "Acme/Api#7",
		"T01ABC/C02DEF/1700000000.000100", "T01ABC/D02DEF/seg/a-1", "T01ABC/X02/1.2", "t01/C02/1.2", "T01ABC/C02DEF/seg/",
		"T01ABC/C02DEF", " ", "acme/api#3 "}
	urls := []string{"https://github.com/acme/api/pull/12", "http://github.com/acme/api/pull/12/files", "https://github.com/acme/api/pull/0",
		"https://github.com/acme/api/issues/3", "https://github.com/acme/api/pull/12?x=1#y", "https://gitlab.com/acme/api/pull/1"}
	for _, old := range []unit.Codec{pr.Codec{}, dslack.Codec()} {
		got := cs[old.Provider()]
		if got == nil {
			t.Fatalf("no manifest codec of %s", old.Provider())
		}
		for _, k := range keys {
			if (old.Valid(k) == nil) != (got.Valid(k) == nil) {
				t.Errorf("%s Valid(%q): Go %v, manifest %v", old.Provider(), k, old.Valid(k), got.Valid(k))
			}
			if old.Valid(k) != nil {
				continue
			}
			want := unit.Unit{Provider: old.Provider(), Key: k}.String()
			if old.Provider() == unit.GitHub {
				want = k
			}
			if have := got.(unit.Labeler).Label(k); want != have {
				t.Errorf("%s Label(%q): %q, manifest %q", old.Provider(), k, want, have)
			}
		}
		if l, ok := old.(unit.Linker); ok != unit.NewRegistry(got).Linked(old.Provider()) {
			t.Errorf("%s: Go codec links %t, manifest's does not agree", old.Provider(), ok)
		} else if ok {
			gl := got.(unit.Linker)
			for _, k := range keys {
				if old.Valid(k) == nil && l.URL(k) != gl.URL(k) {
					t.Errorf("URL(%q): %q, manifest %q", k, l.URL(k), gl.URL(k))
				}
			}
			for _, u := range urls {
				wk, wok := l.KeyOf(u)
				hk, hok := gl.KeyOf(u)
				if wk != hk || wok != hok {
					t.Errorf("KeyOf(%q): %q %t, manifest %q %t", u, wk, wok, hk, hok)
				}
			}
		}
		for _, pair := range [][2]string{{"acme/api#1", "ACME/api#1"}, {"T01ABC/C02DEF/1.2", "t01abc/c02def/1.2"}} {
			if unit.KeysEqual(old, pair[0], pair[1]) != unit.KeysEqual(got, pair[0], pair[1]) {
				t.Errorf("%s Equal%v differs", old.Provider(), pair)
			}
		}
		if old.(unit.Targeter).Target() != got.(unit.Targeter).Target() {
			t.Errorf("%s Target: %q, manifest %q", old.Provider(), old.(unit.Targeter).Target(), got.(unit.Targeter).Target())
		}
	}
}
