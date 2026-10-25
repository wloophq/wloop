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

// Cut reads the leading word of s as Word or Quote wrote it, and returns it
// unquoted with the rest of s; ok is false for a word they would not write.
func Cut(s string) (word, rest string, ok bool) {
	if !strings.HasPrefix(s, "'") {
		i := strings.IndexByte(s, ' ')
		if i < 0 {
			i = len(s)
		}
		return s[:i], s[i:], i > 0 && Word(s[:i]) == s[:i]
	}
	var b strings.Builder
	s = s[1:]
	for {
		part, after, closed := strings.Cut(s, "'")
		if !closed {
			return "", "", false
		}
		b.WriteString(part)
		if escaped, more := strings.CutPrefix(after, `\''`); more {
			b.WriteByte('\'')
			s = escaped
			continue
		}
		if after != "" && after[0] != ' ' {
			return "", "", false
		}
		return b.String(), after, true
	}
}
