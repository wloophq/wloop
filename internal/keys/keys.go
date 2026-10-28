// Package keys is the one keyboard map of every surface: the core bindings
// and the rule plugin and intent keys follow.
package keys

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

type Surface string

const (
	Web     Surface = "web"
	TUI     Surface = "tui"
	Menubar Surface = "menubar"
)

type Context string

const (
	Global   Context = "global"
	List     Context = "list"
	Question Context = "question"
	Review   Context = "review"
	Plan     Context = "plan"
)

type Binding struct {
	Key      string // canonical: "x", "X" (Shift+x), "Enter", "Esc", "Backspace", "Tab", "Ctrl+K", "Cmd+K", "?", "/", "g f", "1".."9", "Shift+1".."Shift+9"
	Action   string // task verb, "nav:<view>", "area:<n>", "answer:<n>", "move:down", "palette", "help", "filter", "open-source", "capture", ...
	Context  Context
	Surfaces []Surface
}

var named = map[string]string{
	"enter": "Enter", "return": "Enter", "esc": "Esc", "escape": "Esc",
	"backspace": "Backspace", "tab": "Tab", "space": "Space",
}

// Canonical turns a key name into its canonical form ("shift+r" -> "R"); it rejects unknown names.
func Canonical(key string) (string, error) {
	parts := strings.Split(key, " ")
	if len(parts) == 2 {
		prefix, err1 := single(parts[0])
		rest, err2 := single(parts[1])
		if err1 != nil || err2 != nil || !lowerLetter(prefix) {
			return "", fmt.Errorf("%q is not a key: a chord is a lowercase letter, a space and a key", key)
		}
		return prefix + " " + rest, nil
	}
	if len(parts) > 2 {
		return "", fmt.Errorf("%q is not a key", key)
	}
	return single(key)
}

func single(key string) (string, error) {
	if len(key) == 1 && key[0] > ' ' && key[0] < 0x7f {
		return key, nil
	}
	if n, ok := named[strings.ToLower(key)]; ok {
		return n, nil
	}
	mod, rest, ok := strings.Cut(key, "+")
	if !ok || len(rest) != 1 {
		return "", fmt.Errorf("%q is not a key: name one character, Enter, Esc, Backspace, Tab, Space, or Shift, Ctrl or Cmd plus one character", key)
	}
	c := rest[0]
	switch strings.ToLower(mod) {
	case "shift":
		switch {
		case isLetter(c):
			return strings.ToUpper(rest), nil
		case c >= '0' && c <= '9':
			return "Shift+" + rest, nil
		}
	case "ctrl", "cmd":
		if isLetter(c) {
			return strings.ToUpper(mod[:1]) + strings.ToLower(mod[1:]) + "+" + strings.ToUpper(rest), nil
		}
	}
	return "", fmt.Errorf("%q is not a key: Shift takes a letter or a digit, Ctrl and Cmd a letter, and no other modifier is known", key)
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func lowerLetter(k string) bool { return len(k) == 1 && k[0] >= 'a' && k[0] <= 'z' }

func upperLetter(k string) bool { return len(k) == 1 && k[0] >= 'A' && k[0] <= 'Z' }

// Reserved is every Core key on s, every chord prefix ("g"), every lowercase ASCII letter and digit.
func Reserved(s Surface) map[string]bool { return maps.Clone(reserved[s]) }

var reserved, coreAction = func() (map[Surface]map[string]bool, map[Surface]map[string]string) {
	r, a := map[Surface]map[string]bool{}, map[Surface]map[string]string{}
	for _, s := range []Surface{Web, TUI, Menubar} {
		r[s], a[s] = map[string]bool{}, map[string]string{}
		for _, b := range Core {
			if slices.Contains(b.Surfaces, s) {
				r[s][b.Key] = true
				if _, ok := a[s][b.Key]; !ok {
					a[s][b.Key] = b.Action
				}
			}
		}
		for _, p := range chordPrefixes {
			r[s][p] = true
		}
		for c := 'a'; c <= 'z'; c++ {
			r[s][string(c)] = true
		}
		for c := '0'; c <= '9'; c++ {
			r[s][string(c)] = true
		}
	}
	return r, a
}()

var ErrReserved = errors.New("key is reserved by the core")

type reservedError struct{ msg string }

func (e reservedError) Error() string        { return e.msg }
func (e reservedError) Is(target error) bool { return target == ErrReserved }

// Check refuses a plugin or intent key: not one character of A-Z, or reserved on s, or taken on s by an owner
// that is not its toggle pair. claimed maps key -> owner id; toggles maps an action id to its Toggle partner.
// The error names the key, the surface and the owner.
func Check(s Surface, key, owner string, claimed map[string]string, toggles map[string]string) error {
	k, err := Canonical(key)
	if err != nil {
		return fmt.Errorf("%w on %s", err, s)
	}
	if reserved[s][k] {
		if action, ok := coreAction[s][k]; ok {
			return reservedError{fmt.Sprintf("%s is the core key of %s on %s", k, action, s)}
		}
		return reservedError{fmt.Sprintf("%s is reserved by the core on %s", k, s)}
	}
	if !upperLetter(k) {
		return fmt.Errorf("%s is not one uppercase letter A-Z on %s", k, s)
	}
	if other, ok := claimed[k]; ok && other != owner && toggles[owner] != other && toggles[other] != owner {
		return fmt.Errorf("%s is the key of %s on %s", k, other, s)
	}
	return nil
}

// SurfacesOf maps the surfaces of an action spec (row, detail, tui, menubar, ...) to the keyboard surfaces they reach.
func SurfacesOf(actionSurfaces []string) []Surface {
	var out []Surface
	for _, s := range []Surface{Web, TUI, Menubar} {
		for _, a := range actionSurfaces {
			if keyboardOf[a] == s {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

var keyboardOf = map[string]Surface{"row": Web, "detail": Web, "tui": TUI, "menubar": Menubar}
