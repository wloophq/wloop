package pr

import "testing"

func TestParseURL(t *testing.T) {
	cases := []struct {
		in   string
		want Ref
		err  bool
	}{
		{in: "https://github.com/acme/api/pull/7", want: Ref{"acme", "api", 7}},
		{in: " https://github.com/acme/api/pull/7/files ", want: Ref{"acme", "api", 7}},
		{in: "https://github.com/acme/api/pull/7#discussion_r1", want: Ref{"acme", "api", 7}},
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
