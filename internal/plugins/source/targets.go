package source

import "github.com/wloophq/wloop/internal/core/port"

var _ port.Targeter = Publisher{}

// Takes says whether the plugin posts drafts to target: whether its
// manifest codec's targets pattern matches it.
func (p Publisher) Takes(target string) bool {
	c, err := p.Source.Codec.Compile()
	return err == nil && c.Takes(target)
}
