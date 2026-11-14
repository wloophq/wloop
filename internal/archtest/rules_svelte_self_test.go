package archtest

import (
	"slices"
	"strings"
	"testing"
)

func TestSvelteProseFlagsAPlantedText(t *testing.T) {
	src := `<script lang="ts">
  import { t } from "./i18n.svelte";
  let { row } = $props();
  const label = "Open cockpit";
  const ok = t("Open cockpit");
</script>

<div class="row muted" data-focus="row:1">
  <button title="Do it now" aria-label={t("Reload")} onclick={() => run("Close it")}>{t("Reload")}</button>
  {#if row.open}
    <span>Waiting on you</span>
  {:else}
    <span>{row.title} · {tn("{n} task", "{n} tasks", 2)}</span>
  {/if}
  <input placeholder="Filter the list {x}" type="search">
  <p>{"Some words"}</p>
</div>
`
	got := ruleSvelteProse("internal/web/ui/src/X.svelte", src)
	flagged(t, "svelte", got, 6, "t or tn")
	for want, line := range map[string]string{"Open cockpit": ":4 ", "Do it now": ":9:", "Close it": ":9 ", "Waiting on you": ":11:",
		"Filter the list": ":15:", "Some words": ":16 "} {
		if !slices.ContainsFunc(got, func(g string) bool { return strings.Contains(g, want) && strings.Contains(g, line) }) {
			t.Errorf("%q on line %s is not flagged: %q", want, line, got)
		}
	}
	lits := cockpitLiterals("X.svelte", src)
	var keys []string
	for _, l := range lits {
		if jsKeyCall.MatchString(l.before) {
			keys = append(keys, l.text)
		}
	}
	if want := []string{"Open cockpit", "Reload", "Reload", "{n} task"}; !slices.Equal(keys, want) {
		t.Errorf("the keys read: %q, want %q", keys, want)
	}
	for rel, want := range map[string]bool{"App.svelte": true, "state/store.svelte.ts": true, "state/store.test.ts": false,
		"testing/fixtures.ts": false, "app.css": false} {
		if got := isCockpitSource(rel); got != want {
			t.Errorf("isCockpitSource(%q) = %v", rel, got)
		}
	}
}
