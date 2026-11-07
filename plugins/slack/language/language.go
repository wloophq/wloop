// Package language tells the language a Slack thread's people write in.
package language

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/abadojack/whatlanggo"
)

// minLetters is the fewest letters a thread's text holds for Detect to answer: below it the detector's guess
// on short chat text is wrong too often, and a wrong language costs more than the user's own.
const minLetters = 40

// minConfidence is the lowest lead of the detector's best guess over its next one that Detect trusts; the
// detector's own reliability bar refuses most one-line threads in Portuguese, whose next guess is a close
// language such as Spanish or Galician.
const minConfidence = 0.3

// maxBytes bounds the text Detect reads, from its end: the detector's cost grows with the text, and a few
// sentences tell the language as well as a whole thread.
const maxBytes = 1024

// candidates are the languages Detect answers: widely written ones the detector tells apart in chat text.
// Over all its languages, a short mixed thread can lead with one nobody there writes, such as Haitian Creole.
var candidates = map[whatlanggo.Lang]bool{
	whatlanggo.Eng: true, whatlanggo.Por: true, whatlanggo.Spa: true, whatlanggo.Fra: true, whatlanggo.Deu: true,
	whatlanggo.Ita: true, whatlanggo.Nld: true, whatlanggo.Pol: true, whatlanggo.Rus: true, whatlanggo.Ukr: true,
	whatlanggo.Tur: true, whatlanggo.Jpn: true, whatlanggo.Cmn: true, whatlanggo.Kor: true, whatlanggo.Arb: true,
	whatlanggo.Hin: true, whatlanggo.Swe: true, whatlanggo.Dan: true, whatlanggo.Nob: true, whatlanggo.Fin: true,
	whatlanggo.Ces: true, whatlanggo.Ron: true, whatlanggo.Hun: true, whatlanggo.Ell: true, whatlanggo.Heb: true,
	whatlanggo.Ind: true, whatlanggo.Vie: true, whatlanggo.Tha: true,
}

var noise = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`|<[@#!][^>]*>|<https?:[^>]*>|https?://\\S+|:[a-z0-9_+'-]+:")

// Detect is the ISO 639-1 code of the language texts are written in; "" when the detector is not sure or
// too few letters remain once mentions, channel links, links, emoji codes and code are left out.
func Detect(texts []string) string {
	var b strings.Builder
	for _, t := range slices.Backward(texts) {
		if b.Len() >= maxBytes {
			break
		}
		b.WriteString(noise.ReplaceAllString(t, " "))
		b.WriteByte('\n')
	}
	text := b.String()
	if len(text) > maxBytes {
		text = strings.ToValidUTF8(text[:maxBytes], "")
	}
	letters := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < minLetters {
		return ""
	}
	info := whatlanggo.DetectWithOptions(text, whatlanggo.Options{Whitelist: candidates})
	if info.Confidence < minConfidence {
		return ""
	}
	return whatlanggo.LangToStringShort(info.Lang)
}
