package brief

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

var checkbox = regexp.MustCompile(`(?m)^(\s*(?:[-*+]|\d+[.)])\s+)\[[xX ]\]`)

// InputSHA hashes what a brief is made from, the head included: the facts
// with every task-list box read as unticked, so ticking one changes nothing.
// settings are the other inputs of the call that the version does not cover.
func InputSHA(head, settings string, in Input) string {
	h := sha256.Sum256([]byte(strings.Join([]string{head, settings, in.Title, checkbox.ReplaceAllString(in.Body, "$1[ ]"), in.Issue, strings.Join(in.Files, "\n")}, "\x00")))
	return hex.EncodeToString(h[:])
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
