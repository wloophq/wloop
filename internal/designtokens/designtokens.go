// Package designtokens reads design/tokens.json, the platform-neutral source
// of the visual tokens, and renders the cockpit's CSS custom properties
// from it.
package designtokens

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Begin and End delimit the generated region of a stylesheet.
const (
	Begin = "/* Generated from design/tokens.json by `go generate ./internal/web`. Do not edit. */"
	End   = "/* End of generated tokens. */"

	BundleHeader = "/* Generated from design/tokens.json, internal/web/static/app.css and design/components/preview.css by `go generate ./internal/web`. Do not edit. */"
)

// Tokens is the part of tokens.json that surfaces render.
type Tokens struct {
	Color struct {
		Themes []struct {
			ID string `json:"id"`
		} `json:"themes"`
		Tokens []struct {
			Name  string     `json:"name"`
			Value themeValue `json:"value"`
		} `json:"tokens"`
	} `json:"color"`
	Type struct {
		Families map[string]string `json:"families"`
		Groups   []struct {
			Styles []struct {
				Name     string `json:"name"`
				FontSize string `json:"fontSize"`
			} `json:"styles"`
		} `json:"groups"`
	} `json:"type"`
	Text    family `json:"text"`
	Spacing family `json:"spacing"`
	Radius  family `json:"radius"`
	Layout  family `json:"layout"`
}

type family struct {
	Tokens []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"tokens"`
}

// themeValue is a color per theme id; a plain string is the first theme's.
type themeValue map[string]string

func (v *themeValue) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*v = themeValue{"": s}
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*v = m
	return nil
}

func (v themeValue) in(theme string, first bool) (string, bool) {
	if s, ok := v[theme]; ok {
		return s, true
	}
	s, ok := v[""]
	return s, ok && first
}

// Parse reads tokens.json. The cockpit has a light theme and a dark one, in
// that order, and every type style's size is a step of the text scale.
func Parse(raw []byte) (Tokens, error) {
	var t Tokens
	if err := json.Unmarshal(raw, &t); err != nil {
		return t, err
	}
	if len(t.Color.Themes) != 2 || t.Color.Themes[0].ID != "light" || t.Color.Themes[1].ID != "dark" {
		return t, errors.New("color.themes must be light, then dark")
	}
	sizes := map[string]bool{}
	for _, s := range t.Text.Tokens {
		sizes[s.Value] = true
	}
	for _, g := range t.Type.Groups {
		for _, s := range g.Styles {
			if !sizes[s.FontSize] {
				return t, fmt.Errorf("type style %s: fontSize %s is not on the text scale", s.Name, s.FontSize)
			}
		}
	}
	return t, nil
}

// CSS renders the generated region, markers included: the light colors and
// font families, the size scales, then the dark colors.
func (t Tokens) CSS() string {
	var b strings.Builder
	b.WriteString(Begin + "\n:root {\n")
	for _, c := range t.Color.Tokens {
		if v, ok := c.Value.in("light", true); ok {
			fmt.Fprintf(&b, "  --%s: %s;\n", c.Name, v)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(t.Type.Families)) {
		fmt.Fprintf(&b, "  --%s: %s;\n", name, t.Type.Families[name])
	}
	b.WriteString("  color-scheme: light;\n}\n\n" + t.scale() + "\n@media (prefers-color-scheme: dark) {\n  :root {\n")
	for _, c := range t.Color.Tokens {
		if v, ok := c.Value.in("dark", false); ok {
			fmt.Fprintf(&b, "    --%s: %s;\n", c.Name, v)
		}
	}
	b.WriteString("    color-scheme: dark;\n  }\n}\n" + End)
	return b.String()
}

// scale renders the size scales, which hold in every theme.
func (t Tokens) scale() string {
	var b strings.Builder
	b.WriteString(":root {\n")
	for _, f := range []family{t.Text, t.Radius, t.Spacing, t.Layout} {
		for _, s := range f.Tokens {
			// A custom property cannot be read by a media query.
			if strings.HasPrefix(s.Name, "breakpoint-") {
				continue
			}
			fmt.Fprintf(&b, "  --%s: %s;\n", s.Name, s.Value)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// Splice replaces the region between Begin and End in css with region.
func Splice(css []byte, region string) ([]byte, error) {
	before, rest, ok := bytes.Cut(css, []byte(Begin))
	if !ok {
		return nil, errors.New("no begin marker")
	}
	_, after, ok := bytes.Cut(rest, []byte(End))
	if !ok {
		return nil, errors.New("no end marker")
	}
	return slices.Concat(before, []byte(region), after), nil
}

// Bundle renders the design system's component stylesheet: the size scales,
// the cockpit's rules after its generated region, then the rules only the
// previews load. The design system's page supplies the colors per theme.
func Bundle(t Tokens, appCSS, preview []byte) ([]byte, error) {
	_, rules, ok := bytes.Cut(appCSS, []byte(End))
	if !ok {
		return nil, errors.New("no end marker")
	}
	return slices.Concat([]byte(BundleHeader+"\n"+t.scale()+"\n"), bytes.TrimLeft(rules, "\n"), []byte("\n"), preview), nil
}
