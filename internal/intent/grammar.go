package intent

import (
	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

// grammar is what the grammar table refuses in src, a file of surface:
// unknown keys and values, kinds and enums. A file that does not parse has
// none: its syntax error is decode's.
func grammar(surface builtin.Surface, src []byte) []builtin.Problem {
	var doc map[string]any
	if toml.Unmarshal(src, &doc) != nil {
		return nil
	}
	return builtin.Check(surface, doc)
}

// addGrammar adds the grammar's problems at the paths no problem of the
// sheet names yet, which say more about their key than the grammar's.
func (s *sheet) addGrammar(ps []builtin.Problem) {
	named := map[string]bool{}
	for _, p := range s.problems {
		named[p.Field] = true
	}
	for _, p := range ps {
		if !named[p.Path] {
			s.add(p.Path, "%s", p.Msg)
		}
	}
}
