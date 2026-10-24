package web

import (
	"strings"
	"testing"
	"time"
)

func TestDashTooltipsEscapeTitlesAndTags(t *testing.T) {
	now := time.Date(2026, 10, 19, 15, 0, 0, 0, time.UTC)
	var got []string
	rows := dashRows(now)
	for _, r := range rows {
		r["title"] = "<img src=x onerror=alert(1)>"
		if b, ok := r["brief"].(map[string]any); ok {
			for i, tag := range b["tags"].([]string) {
				if tag == "area:auth" {
					b["tags"].([]string)[i] = "area:<b>x</b>"
				}
			}
		}
	}
	dashRun(t, "", rows, now, `
const tips = [...dashAgingOption(rows, lens, pal, now, 48).series[0].data, ...dashLiftOption(rows, lens, pal).series[0].data]
  .map((p) => p.tooltip.formatter);
process.stdout.write(JSON.stringify(tips));`, &got)
	var title, tag bool
	for _, tip := range got {
		if strings.ContainsAny(tip, "<>") {
			t.Errorf("unescaped tooltip %q", tip)
		}
		title = title || strings.Contains(tip, "&lt;img src=x onerror=alert(1)&gt;")
		tag = tag || strings.Contains(tip, "area:&lt;b&gt;x&lt;/b&gt;")
	}
	if !title || !tag {
		t.Fatalf("title %v, tag %v in %q", title, tag, got)
	}
}
