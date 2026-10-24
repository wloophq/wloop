package web

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var tokenRe = regexp.MustCompile(`--([\w-]+):\s*(#[0-9a-fA-F]{6})`)

func themeTokens(t *testing.T) (light, dark map[string]string) {
	t.Helper()
	b, err := files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	lightCSS, darkCSS, ok := strings.Cut(string(b), "@media (prefers-color-scheme: dark)")
	if !ok {
		t.Fatal("no dark theme block")
	}
	darkCSS, _, _ = strings.Cut(darkCSS, "\n}\n")
	light, dark = map[string]string{}, map[string]string{}
	for _, m := range tokenRe.FindAllStringSubmatch(lightCSS, -1) {
		light[m[1]], dark[m[1]] = m[2], m[2]
	}
	for _, m := range tokenRe.FindAllStringSubmatch(darkCSS, -1) {
		dark[m[1]] = m[2]
	}
	return light, dark
}

func luminance(hex string) float64 {
	var c [3]float64
	for i := range c {
		v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		x := float64(v) / 255
		if x <= 0.03928 {
			c[i] = x / 12.92
		} else {
			c[i] = math.Pow((x+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// TestTextColorsMeetAA checks every text token against the backgrounds it is
// drawn on, in both themes, for the WCAG AA small-text minimum.
func TestTextColorsMeetAA(t *testing.T) {
	surfaces := []string{"bg", "panel", "hover", "sel", "mine-bg"}
	pairs := map[string][]string{
		"ink":           slices.Concat(surfaces, []string{"mark"}),
		"muted":         slices.Concat(surfaces, []string{"late-bg", "err-bg"}),
		"me-text":       surfaces,
		"author-text":   surfaces,
		"reviewer-text": surfaces,
		"loop-text":     surfaces,
		"none-text":     surfaces,
		"late":          slices.Concat(surfaces, []string{"late-bg"}),
		"err":           {"err-bg", "panel"},
		"mine-text":     surfaces,
		"on-me":         {"me"},
	}
	light, dark := themeTokens(t)
	for theme, tokens := range map[string]map[string]string{"light": light, "dark": dark} {
		for fg, bgs := range pairs {
			for _, bg := range bgs {
				f, b := tokens[fg], tokens[bg]
				if f == "" || b == "" {
					t.Errorf("%s: missing --%s or --%s", theme, fg, bg)
					continue
				}
				if r := contrast(f, b); r < 4.5 {
					t.Errorf("%s: --%s on --%s is %.2f:1", theme, fg, bg, r)
				}
			}
		}
	}
}

// TestStripColorsTellApart keeps the author and nobody strips, both grays,
// apart in both themes, and every strip visible on the surfaces a row or
// card sits on.
func TestStripColorsTellApart(t *testing.T) {
	light, dark := themeTokens(t)
	for theme, tokens := range map[string]map[string]string{"light": light, "dark": dark} {
		if r := contrast(tokens["author"], tokens["none"]); r < 2.5 {
			t.Errorf("%s: --author and --none strips are %.2f:1 apart", theme, r)
		}
		for _, strip := range []string{"me", "author", "reviewer", "loop", "none"} {
			for _, bg := range []string{"panel", "bg", "mine-bg"} {
				if r := contrast(tokens[strip], tokens[bg]); r < 1.5 {
					t.Errorf("%s: --%s strip on --%s is %.2f:1", theme, strip, bg, r)
				}
			}
		}
	}
}
