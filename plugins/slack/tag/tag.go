// Package tag names what a pull request touches with key:value tags, a
// matching system and nothing more: nine fixed keys, each value normalized
// to one spelling.
package tag

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type Key struct {
	Name string
	// Closed lists the values a closed key accepts; nil for an open key.
	Closed []string
	// Seeds are the open key's usual values, which the vocabulary lists
	// after the ones in use.
	Seeds []string
}

func (k Key) Open() bool { return k.Closed == nil }

var Keys = []Key{
	{Name: "lang", Seeds: []string{"python", "go", "typescript", "javascript", "java", "kotlin", "ruby", "rust", "php", "csharp", "cpp", "c", "sql", "shell", "hcl", "yaml", "html", "css", "markdown"}},
	{Name: "framework", Seeds: []string{"django", "flask", "fastapi", "react", "nextjs", "vue", "rails", "spring", "express"}},
	{Name: "tool", Seeds: []string{"docker", "kubernetes", "terraform", "github-actions", "helm", "make", "npm", "poetry"}},
	{Name: "area", Closed: []string{"api", "ui", "cli", "data-model", "migration", "ci", "build", "deps", "docs", "tests", "config", "infra", "observability", "auth", "storage", "messaging", "jobs"}},
	{Name: "change", Closed: []string{"feature", "fix", "refac", "perf", "docs", "test", "deps", "config", "chore", "revert", "rename", "removal", "migration", "security"}},
	{Name: "concern", Closed: []string{"security", "privacy", "compliance", "performance", "data-loss", "concurrency", "backward-compat", "accessibility", "i18n", "cost", "reliability"}},
	{Name: "breaking", Closed: []string{"api", "schema", "config", "behavior", "cli"}},
	{Name: "domain"},
	{Name: "audience", Closed: []string{"end-user", "internal", "developer", "operator"}},
}

const (
	MaxPerKey = 3
	Max       = 16
	// maxBytes bounds one value.
	maxBytes = 32
)

type Count struct {
	Value string
	Count int
}

func lookup(name string) (Key, bool) {
	i := slices.IndexFunc(Keys, func(k Key) bool { return k.Name == name })
	if i < 0 {
		return Key{}, false
	}
	return Keys[i], true
}

// OpenKeys names the open keys, in Keys order.
func OpenKeys() []string {
	var out []string
	for _, k := range Keys {
		if k.Open() {
			out = append(out, k.Name)
		}
	}
	return out
}

var latin = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "å", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n", "ý", "y", "ÿ", "y", "ß", "ss", "æ", "ae", "œ", "oe",
)

var exact = map[string]string{"c++": "cpp", "c#": "csharp", "f#": "fsharp", ".net": "dotnet", "node.js": "nodejs"}

var openAliases = map[string]string{
	"golang": "go", "py": "python", "python3": "python", "ts": "typescript", "js": "javascript",
	"k8s": "kubernetes", "postgres": "postgresql",
}

var aliases = map[string]map[string]string{
	"lang":      openAliases,
	"framework": openAliases,
	"tool":      openAliases,
	"area":      {"documentation": "docs", "test": "tests"},
	"change": {
		"refactor": "refac", "refactoring": "refac", "bugfix": "fix", "bug": "fix", "feat": "feature",
		"documentation": "docs", "tests": "test",
	},
}

var (
	separators = regexp.MustCompile(`[ _./]+`)
	outside    = regexp.MustCompile(`[^a-z0-9-]`)
	dashes     = regexp.MustCompile(`-+`)
	valid      = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
)

// Normalize gives value's one spelling under key, false when key is not one
// of Keys or the value does not make a tag.
func Normalize(key, value string) (string, bool) {
	k, ok := lookup(key)
	if !ok {
		return "", false
	}
	v := latin.Replace(strings.ToLower(strings.TrimSpace(value)))
	if e, ok := exact[v]; ok {
		v = e
	}
	v = separators.ReplaceAllString(v, "-")
	v = outside.ReplaceAllString(v, "")
	v = strings.Trim(dashes.ReplaceAllString(v, "-"), "-")
	if a, ok := aliases[k.Name][v]; ok {
		v = a
	}
	if len(v) > maxBytes || !valid.MatchString(v) || !k.Open() && !slices.Contains(k.Closed, v) {
		return "", false
	}
	return v, true
}

// Parse splits a stored key:value tag; ok only when it is already normalized.
func Parse(s string) (key, value string, ok bool) {
	key, value, found := strings.Cut(s, ":")
	if !found {
		return "", "", false
	}
	if n, valid := Normalize(key, value); !valid || n != value {
		return "", "", false
	}
	return key, value, true
}

// FromObject normalizes the brief's tag object into key:value tags, in Keys
// order and then the model's, at most MaxPerKey per key and Max in all;
// dropped lists, as key:value, what was refused or over a limit.
func FromObject(raw map[string][]string) (tags, dropped []string) {
	for _, k := range Keys {
		n := 0
		for _, v := range raw[k.Name] {
			norm, ok := Normalize(k.Name, v)
			t := k.Name + ":" + norm
			switch {
			case !ok, n == MaxPerKey, len(tags) == Max:
				dropped = append(dropped, k.Name+":"+v)
			case slices.Contains(tags, t):
			default:
				tags = append(tags, t)
				n++
			}
		}
	}
	var unknown []string
	for name, vs := range raw {
		if _, ok := lookup(name); !ok {
			for _, v := range vs {
				unknown = append(unknown, name+":"+v)
			}
		}
	}
	slices.Sort(unknown)
	return tags, append(dropped, unknown...)
}

// WithSeeds adds to v each open key's seeds that v does not list, with a
// zero count, up to limit values per key.
func WithSeeds(v map[string][]Count, limit int) map[string][]Count {
	out := map[string][]Count{}
	for _, k := range Keys {
		cs := slices.Clone(v[k.Name])
		for _, s := range k.Seeds {
			if len(cs) >= limit {
				break
			}
			if !slices.ContainsFunc(cs, func(c Count) bool { return c.Value == s }) {
				cs = append(cs, Count{Value: s})
			}
		}
		if len(cs) > 0 {
			out[k.Name] = cs
		}
	}
	return out
}

// Vocabulary lists, one open key per line in Keys order, the values in use
// with their counts, then the unused seeds, in the order given.
func Vocabulary(v map[string][]Count) string {
	var lines []string
	for _, k := range Keys {
		cs := v[k.Name]
		if !k.Open() || len(cs) == 0 {
			continue
		}
		vals := make([]string, len(cs))
		for i, c := range cs {
			vals[i] = c.Value
			if c.Count > 0 {
				vals[i] = fmt.Sprintf("%s (%d)", c.Value, c.Count)
			}
		}
		lines = append(lines, "- "+k.Name+": "+strings.Join(vals, ", "))
	}
	if len(lines) == 0 {
		return "(none yet)"
	}
	return strings.Join(lines, "\n")
}
