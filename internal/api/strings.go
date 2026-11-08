package api

import "encoding/json"

// StringsPath serves the client strings in the daemon's interface language, as Strings.
const StringsPath = "/api/strings"

// Text is one client string as its CLDR plural forms by category ("zero", "one", "two", "few", "many",
// "other"); a string with no plural holds only "other".
type Text map[string]string

// Strings are the client strings resolved for Locale, by key (the English text), and Formats the date and
// number patterns Go clients format with (time, date, datetime as Go layouts; decimal and group separators).
type Strings struct {
	Locale string `json:"locale"`
	// Stamp is the same stamp Health.Strings carries, so a client knows which strings it holds.
	Stamp   string            `json:"stamp"`
	Strings map[string]Text   `json:"strings"`
	Formats map[string]string `json:"formats"`
}

// MarshalJSON writes a text with only an "other" form as a bare string, else the object of its forms.
func (t Text) MarshalJSON() ([]byte, error) {
	if len(t) == 1 {
		if s, ok := t["other"]; ok {
			return json.Marshal(s)
		}
	}
	return json.Marshal(map[string]string(t))
}

// UnmarshalJSON reads a bare string as the "other" form, else the object of forms.
func (t *Text) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*t = Text{"other": s}
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*t = m
	return nil
}
