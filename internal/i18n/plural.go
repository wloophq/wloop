package i18n

// Forms are a text's CLDR plural forms by category ("zero", "one", "two", "few", "many", "other"); "other"
// is required.
type Forms map[string]string

// Form is the CLDR plural category of n in locale.
func Form(locale string, n float64) string {
	panic("not implemented: D1")
}

// For is the text of n's form in locale, else the "other" text.
func (f Forms) For(locale string, n float64) string {
	panic("not implemented: D1")
}
