package intent

import (
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/i18n"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

// StringsFile holds the texts the clients show, by their English text, and the formats of the Go clients.
const StringsFile = "strings.toml"

type mStrings struct {
	Strings map[string]any    `toml:"strings"`
	Formats map[string]string `toml:"formats"`
}

// loadStrings reads strings.toml from bundled and every other layer, each text in r's locale: a higher
// layer's text or format replaces a lower one's, and a text that fails keeps the lower one.
func loadStrings(layers []profile.Layer, r layerReader) (map[string]i18n.Forms, map[string]string, Problems) {
	texts, formats := map[string]i18n.Forms{}, map[string]string{}
	problems, _ := readLayered(layers, r, StringsFile, func(file string, src []byte) Problems {
		return mergeStrings(texts, formats, file, src)
	})
	if r.texts != nil {
		for key := range texts {
			if f, ok := r.texts.Forms(StringsFile, []string{"strings", key}); ok && strings.TrimSpace(f["other"]) != "" {
				texts[key] = f
			}
		}
	}
	return texts, formats, problems
}

func mergeStrings(texts map[string]i18n.Forms, formats map[string]string, file string, src []byte) Problems {
	m, problems, _, complete := decode[mStrings](file, src)
	if !complete {
		return problems
	}
	s := &sheet{file: file, at: positions(src), problems: problems}
	for _, key := range slices.Sorted(maps.Keys(m.Strings)) {
		if f, ok := formsOf(s, "strings."+key, m.Strings[key]); ok {
			texts[key] = f
		}
	}
	for _, key := range slices.Sorted(maps.Keys(m.Formats)) {
		if strings.TrimSpace(m.Formats[key]) == "" {
			s.add("formats."+key, "is required")
			continue
		}
		formats[key] = m.Formats[key]
	}
	s.addGrammar(grammar(builtin.SurfaceStrings, src))
	return s.problems
}

// formsOf is v, a text at path, as its plural forms: a string is its "other" form.
func formsOf(s *sheet, path string, v any) (i18n.Forms, bool) {
	switch x := v.(type) {
	case string:
		if strings.TrimSpace(x) == "" {
			s.add(path, "is required")
			return nil, false
		}
		return i18n.Forms{"other": x}, true
	case map[string]any:
		out := i18n.Forms{}
		for form, t := range x {
			str, ok := t.(string)
			if !ok {
				s.add(path+"."+form, "must be a string")
				return nil, false
			}
			if slices.Contains(builtin.PluralForms, form) {
				out[form] = str
			}
		}
		if strings.TrimSpace(out["other"]) == "" {
			s.add(path, "needs other: the text of every count no other form names")
			return nil, false
		}
		return out, true
	}
	s.add(path, "must be a string or a table of plural forms, such as { one = \"{n} draft\", other = \"{n} drafts\" }")
	return nil, false
}

// stringsStamp is a digest of the client strings in locale.
func stringsStamp(locale string, texts map[string]i18n.Forms, formats map[string]string) string {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s\x00", locale)
	for _, k := range slices.Sorted(maps.Keys(texts)) {
		fmt.Fprintf(h, "%s\x00", k)
		for _, form := range slices.Sorted(maps.Keys(texts[k])) {
			fmt.Fprintf(h, "%s=%s\x00", form, texts[k][form])
		}
	}
	for _, k := range slices.Sorted(maps.Keys(formats)) {
		fmt.Fprintf(h, "%s=%s\x00", k, formats[k])
	}
	return locale + "-" + strconv.FormatUint(h.Sum64(), 36)
}
