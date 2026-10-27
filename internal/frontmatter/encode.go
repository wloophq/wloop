package frontmatter

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Value is v written as one TOML value, as go-toml encodes it; a table is
// an error.
func Value(v any) (string, error) {
	raw, err := toml.Marshal(map[string]any{"v": v})
	if err != nil {
		return "", err
	}
	s, ok := strings.CutPrefix(strings.TrimSuffix(string(raw), "\n"), "v = ")
	if !ok || strings.Contains(s, "\n") {
		return "", fmt.Errorf("%T is not a TOML value", v)
	}
	return s, nil
}

// Key is k written as a TOML key, quoted only where it needs it.
func Key(k string) string {
	raw, _ := toml.Marshal(map[string]int{k: 0})
	return strings.TrimSuffix(string(raw), " = 0\n")
}
