package i18n

import (
	"math"
	"strconv"
	"strings"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
)

// Forms are a text's CLDR plural forms by category ("zero", "one", "two", "few", "many", "other"); "other"
// is required.
type Forms map[string]string

var formNames = map[plural.Form]string{
	plural.Other: "other", plural.Zero: "zero", plural.One: "one", plural.Two: "two", plural.Few: "few", plural.Many: "many",
}

// Form is the CLDR plural category of n in locale.
func Form(locale string, n float64) string {
	tag, err := language.Parse(locale)
	if err != nil {
		tag = language.Make(Default)
	}
	digits := strconv.FormatFloat(math.Abs(n), 'f', -1, 64)
	whole, frac, _ := strings.Cut(digits, ".")
	i, err := strconv.Atoi(whole)
	if err != nil {
		i = math.MaxInt32
	}
	f, _ := strconv.Atoi(frac)
	v := len(frac)
	return formNames[plural.Cardinal.MatchPlural(tag, i, v, v, f, f)]
}

// For is the text of n's form in locale, else the "other" text.
func (f Forms) For(locale string, n float64) string {
	if s, ok := f[Form(locale, n)]; ok {
		return s
	}
	return f["other"]
}
