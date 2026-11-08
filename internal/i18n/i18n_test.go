package i18n

import "testing"

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"pt_BR.UTF-8": "pt-BR", "pt-br": "pt-BR", "en_US": "en-US", "pt_BR@euro": "pt-BR", "EN": "en",
		"C": "", "POSIX": "", "C.UTF-8": "", "": "", "  ": "", "!!": "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPick(t *testing.T) {
	avail := []string{"en", "pt-BR"}
	for _, c := range []struct {
		cfg  string
		det  []string
		want string
	}{
		{"", []string{"pt-BR", "en-US"}, "pt-BR"},
		{"", []string{"pt-PT", "en-GB"}, "pt-BR"},
		{"", []string{"pt_BR.UTF-8"}, "pt-BR"},
		{"", []string{"en-GB", "pt-BR"}, "en"},
		{"", []string{"fr-FR"}, "en"},
		{"", []string{"C"}, "en"},
		{"", nil, "en"},
		{"fr", []string{"pt-BR"}, "fr"},
		{"pt_BR", nil, "pt-BR"},
		{"C", []string{"pt-BR"}, "pt-BR"},
	} {
		if got := Pick(c.cfg, c.det, avail); got != c.want {
			t.Errorf("Pick(%q, %q) = %q, want %q", c.cfg, c.det, got, c.want)
		}
	}
	if got := Match([]string{"pt-PT"}, nil); got != Default {
		t.Errorf("Match with nothing available = %q", got)
	}
}

func TestFormFollowsCLDR(t *testing.T) {
	for _, c := range []struct {
		locale string
		n      float64
		want   string
	}{
		{"en", 0, "other"}, {"en", 1, "one"}, {"en", 2, "other"}, {"en", 1.5, "other"},
		{"pt-BR", 0, "one"}, {"pt-BR", 1, "one"}, {"pt-BR", 2, "other"}, {"pt-BR", 1.5, "one"},
		{"fr", 0, "one"}, {"fr", 2, "other"}, {"bogus!", 1, "one"}, {"en", -1, "one"},
	} {
		if got := Form(c.locale, c.n); got != c.want {
			t.Errorf("Form(%q, %v) = %q, want %q", c.locale, c.n, got, c.want)
		}
	}
}

func TestFormsForFallsBackToOther(t *testing.T) {
	f := Forms{"other": "{n} drafts"}
	if got := f.For("pt-BR", 1); got != "{n} drafts" {
		t.Fatalf("%q", got)
	}
	f = Forms{"one": "{n} draft", "other": "{n} drafts"}
	if f.For("en", 1) != "{n} draft" || f.For("en", 2) != "{n} drafts" || f.For("pt-BR", 0) != "{n} draft" {
		t.Fatal(f.For("en", 1), f.For("en", 2), f.For("pt-BR", 0))
	}
}

func TestInterpolate(t *testing.T) {
	for _, c := range []struct {
		text   string
		params map[string]any
		want   string
	}{
		{"{n} of {total}", map[string]any{"n": 3, "total": 7}, "3 of 7"},
		{"{missing} and {n}", map[string]any{"n": 1}, "{missing} and 1"},
		{"{ n } {} {n", map[string]any{"n": 1}, "{ n } {} {n"},
		{"plain", nil, "plain"},
		{"{a}{a}", map[string]any{"a": "x"}, "xx"},
	} {
		if got := Interpolate(c.text, c.params); got != c.want {
			t.Errorf("Interpolate(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}
