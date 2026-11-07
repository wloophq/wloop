// Package locale reads the operating system's preferred languages: macOS's list through CoreFoundation, the
// POSIX locale variables elsewhere. It returns them raw, most preferred first, and decides nothing.
package locale

import (
	"os"
	"strings"
)

// envVars are the POSIX locale variables in the order the C library reads them for messages.
var envVars = []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"}

// fromEnv are the preferred languages getenv names: LANGUAGE's colon list first, then the first of LC_ALL,
// LC_MESSAGES and LANG set; the C and POSIX locales name no language.
func fromEnv(getenv func(string) string) []string {
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		base, _, _ := strings.Cut(v, ".")
		if v != "" && base != "C" && base != "POSIX" {
			out = append(out, v)
		}
	}
	for _, v := range strings.Split(getenv(envVars[0]), ":") {
		add(v)
	}
	for _, name := range envVars[1:] {
		if v := getenv(name); v != "" {
			add(v)
			break
		}
	}
	return out
}

func environment() []string { return fromEnv(os.Getenv) }
