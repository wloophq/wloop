// Package shell quotes words for a POSIX shell command line.
package shell

import "strings"

// Quote is s in single quotes, which sh reads as the one word s.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Word is s as is when sh reads it as the one word s, else Quote(s).
func Word(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	return Quote(s)
}
