package pr

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestParseURL(t *testing.T) {
	cases := []struct {
		in   string
		want Ref
		err  bool
	}{
		{in: "https://github.com/acme/api/pull/7", want: Ref{"acme", "api", 7}},
		{in: " https://github.com/acme/api/pull/7/files ", want: Ref{"acme", "api", 7}},
		{in: "https://github.com/acme/api/pull/7#discussion_r1", want: Ref{"acme", "api", 7}},
		{in: "http://github.com/acme/api/pull/7", want: Ref{"acme", "api", 7}},
		{in: "ftp://github.com/acme/api/pull/7", err: true},
		{in: "https://github.com/acme/api/issues/7", err: true},
		{in: "https://gitlab.com/acme/api/pull/7", err: true},
		{in: "https://github.com/acme/api/pull/0", err: true},
		{in: "acme/api#7", err: true},
	}
	for _, c := range cases {
		got, err := ParseURL(c.in)
		if (err != nil) != c.err || (!c.err && got != c.want) {
			t.Errorf("ParseURL(%q) = %v, %v", c.in, got, err)
		}
	}
}

func TestRefStringAndURL(t *testing.T) {
	r := Ref{Owner: "acme", Repo: "api", Number: 7}
	if r.String() != "acme/api#7" || r.URL() != "https://github.com/acme/api/pull/7" {
		t.Fatal(r.String(), r.URL())
	}
}

func TestRemoteKey(t *testing.T) {
	if got := (Ref{Owner: "acme", Repo: "api", Number: 7}).RemoteKey(); got != "github.com/acme/api" {
		t.Fatalf("remote key %q", got)
	}
}

func TestKeysRoundTripThroughTheCodec(t *testing.T) {
	ref := Ref{Owner: "o", Repo: "r", Number: 12}
	got, err := ParseKey("o/r#12")
	if err != nil || got != ref {
		t.Fatalf("ParseKey = %v %v", got, err)
	}
	if u := ref.Unit(); u.Provider != unit.GitHub || u.Key != "o/r#12" || u.String() != "github:o/r#12" {
		t.Fatalf("Unit = %+v", u)
	}
	reg := unit.NewRegistry(Codec{})
	if back, err := reg.Parse("github", "o/r#12"); err != nil || back != ref.Unit() {
		t.Errorf("Parse = %v %v", back, err)
	}
	for _, link := range []string{"https://github.com/o/r/pull/12", "http://github.com/o/r/pull/12/files"} {
		if u, ok := reg.ParseURL(link); !ok || u != ref.Unit() {
			t.Errorf("ParseURL(%q) = %v %v", link, u, ok)
		}
	}
	for _, key := range []string{"o/r#x", "o/r#012"} {
		if _, err := reg.Parse("github", key); err == nil {
			t.Errorf("%q parsed", key)
		}
	}
}
