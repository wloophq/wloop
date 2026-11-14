<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import { groupedRows, pipeline, rowByURL } from "../../lib/lens";
  import { keyOf } from "../../lib/util";
  import { readLens } from "../../lib/view";
  import { QUERY_MAX, readHash, writeHash } from "../../state/route.svelte";
  import { state } from "../../state/store.svelte";

  // The filter of the list, which the hash keeps as q. A pasted URL of a pull request the inventory holds opens it
  // instead of filtering for text no row contains; a URL typed by hand passes through shorter URLs of other pull
  // requests on its way, so only a paste jumps.
  let pasting = false;
  const q = $derived(readHash().q);

  function onSearch(el: HTMLInputElement, pasted: boolean) {
    const row = pasted && rowByURL(el.value);
    if (row) {
      el.value = "";
      writeHash({ q: "", pr: keyOf(row) });
      return;
    }
    writeHash({ q: el.value.slice(0, QUERY_MAX) }, true);
  }

  function oninput(e: Event & { currentTarget: HTMLInputElement }) {
    const ev = e as unknown as InputEvent;
    // Embedded browsers may insert a paste as plain text, so a paste is also any single input that adds more than
    // one character.
    const pasted = pasting || ev.inputType === "insertFromPaste" || ev.inputType === "insertFromDrop" || (ev.data || "").length > 1;
    pasting = false;
    onSearch(e.currentTarget, pasted);
  }

  function onkeydown(e: KeyboardEvent & { currentTarget: HTMLInputElement }) {
    const box = e.currentTarget;
    if (e.key === "Escape") {
      e.preventDefault();
      if (box.value) {
        box.value = "";
        writeHash({ q: "" }, true);
      } else box.blur();
    } else if (e.key === "Enter") {
      const rows = groupedRows(pipeline(state.rows, readLens()).groups);
      if (rows.length === 1) writeHash({ pr: keyOf(rows[0]) });
    }
  }
</script>

<input
  id="search"
  class="search"
  type="search"
  maxlength="200"
  autocomplete="off"
  spellcheck="false"
  aria-label={t("Filter the list")}
  aria-keyshortcuts="/"
  placeholder={t("Filter: text, where:, author:, wait:me, risk:high, tag:lang:go  ( / )")}
  data-focus="top:search"
  value={q}
  onpaste={() => (pasting = true)}
  {oninput}
  {onkeydown} />
