package api

// StringsPath serves the client strings in the daemon's interface language, as Strings.
const StringsPath = "/api/strings"

// Text is one client string as its CLDR plural forms by category ("zero", "one", "two", "few", "many",
// "other"); a string with no plural holds only "other".
type Text map[string]string

// Strings are the client strings resolved for Locale, by key (the English text), and Formats the date and
// number patterns Go clients format with (time, date, datetime as Go layouts; decimal and group separators).
type Strings struct {
	Locale  string            `json:"locale"`
	Strings map[string]Text   `json:"strings"`
	Formats map[string]string `json:"formats"`
}
