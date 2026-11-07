package locale

import (
	"runtime"
	"slices"
	"testing"
)

func TestPreferredFromEnvironment(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want []string
	}{
		{map[string]string{"LANGUAGE": "pt_BR:en", "LANG": "fr_FR.UTF-8"}, []string{"pt_BR", "en", "fr_FR.UTF-8"}},
		{map[string]string{"LC_ALL": "pt_PT.UTF-8", "LC_MESSAGES": "de_DE", "LANG": "en_US.UTF-8"}, []string{"pt_PT.UTF-8"}},
		{map[string]string{"LC_MESSAGES": "de_DE", "LANG": "en_US.UTF-8"}, []string{"de_DE"}},
		{map[string]string{"LANG": "C"}, nil},
		{map[string]string{"LANG": "C.UTF-8"}, nil},
		{map[string]string{"LANGUAGE": "::", "LC_ALL": "POSIX"}, nil},
		{nil, nil},
	} {
		got := fromEnv(func(k string) string { return c.env[k] })
		if !slices.Equal(got, c.want) {
			t.Errorf("%v: %q, want %q", c.env, got, c.want)
		}
	}
}

func TestPreferredOnDarwinReturnsSomething(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	for _, l := range Preferred() {
		if l == "" {
			t.Fatalf("an empty language in %q", Preferred())
		}
	}
}
