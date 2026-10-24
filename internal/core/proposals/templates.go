package proposals

import (
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/domain/items"
)

func nudgeText(p items.Person, its []items.Item) string {
	return fmt.Sprintf("@{%s}, any news on %s?\n\n%s", p.Key, these(its), bullets(its, nil))
}

func closeText(its []items.Item) string {
	return "Closing this out:\n\n" + bullets(its, func(it items.Item) string {
		if it.Resolution == "" {
			return "resolved"
		}
		return string(it.Resolution)
	})
}

func askText(its []items.Item) string {
	return fmt.Sprintf("Who owns %s?\n\n%s", these(its), bullets(its, nil))
}

func these(its []items.Item) string {
	if len(its) == 1 {
		return "this"
	}
	return "these"
}

func bullets(its []items.Item, suffix func(items.Item) string) string {
	var b strings.Builder
	for i, it := range its {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("- " + oneLine(it.Summary))
		if suffix != nil {
			b.WriteString(": " + suffix(it))
		}
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
