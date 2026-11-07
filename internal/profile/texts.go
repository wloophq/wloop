package profile

import "io/fs"

// LocalesDir holds a layer's translations, locales/<tag>/<rel> mirroring each localizable file rel.
const LocalesDir = "locales"

// TextKeys says whether the key at path of profile file rel holds a text a person reads.
type TextKeys func(rel string, path []string) bool

// TextProblem is a locale file's key Texts refuses.
type TextProblem struct{ File, Key, Msg string }

// Problems are the TextProblems of a localization.
type Problems []TextProblem

// Texts resolves the texts a person reads, per key and per locale, across a profile's layers.
type Texts struct{}

// NewTexts resolves the texts keys names across layers, the bundled layer reading bundled, in locale.
func NewTexts(layers []Layer, bundled fs.FS, locale string, keys TextKeys) *Texts {
	panic("not implemented: D2")
}

// Locale is the locale t resolves texts in.
func (t *Texts) Locale() string {
	panic("not implemented: D2")
}

// Localize is src, layer l's file rel, with each text key's value replaced by Text(rel, path).
func (t *Texts) Localize(l Layer, rel string, src []byte) ([]byte, Problems) {
	panic("not implemented: D2")
}

// Text is the text at path of rel in t's locale, by the layer rule; false when no layer holds it.
func (t *Texts) Text(rel string, path []string) (string, bool) {
	panic("not implemented: D2")
}

// File is the whole-file text rel in t's locale, by the same rule.
func (t *Texts) File(rel string) (Resolved, error) {
	panic("not implemented: D2")
}

// Available are "en" and every locale any layer holds a locales/<tag>/ directory of.
func (t *Texts) Available() []string {
	panic("not implemented: D2")
}
