<script lang="ts">
  import { onMount } from "svelte";
  import { t } from "../../lib/i18n.svelte";
  import type { PaletteItem, PaletteRequest } from "../../state/overlays.svelte";
  import { focusBack } from "./focus";
  import { paletteMatch } from "../../lib/palette";
  import Shade from "./Shade.svelte";

  // A palette of items with their keys and meta: typing narrows them, or asks source for the items of the text
  // typed, the latest text's answer winning; Enter runs the highlighted item of the text typed, once its answer
  // is in; Esc or a click outside closes it.
  let { palette, finish }: { palette: PaletteRequest; finish: () => void } = $props();

  const restore = focusBack();
  let input: HTMLInputElement;
  // svelte-ignore state_referenced_locally
  let shown = $state<PaletteItem[]>(palette.items);
  let at = $state(0);
  let done = false;
  let asked = 0;
  let pending: Promise<void> | null = null;

  function close() {
    if (done) return;
    done = true;
    restore();
    finish();
  }

  function pick(it: PaletteItem | undefined) {
    if (done) return;
    close();
    if (it) it.run();
  }

  function typed() {
    const n = ++asked;
    const q = input.value;
    if (!palette.source) {
      shown = paletteMatch(palette.items, q);
      at = 0;
      return;
    }
    pending = Promise.resolve(palette.source(q)).then((next) => {
      if (n !== asked) return;
      pending = null;
      shown = next || [];
      at = 0;
    });
  }

  async function enter() {
    while (pending) await pending;
    pick(shown[at]);
  }

  function key(e: KeyboardEvent) {
    if (e.key === "Escape") close();
    else if (e.key === "Enter") {
      e.preventDefault();
      void enter();
      return;
    } else if (e.key === "ArrowDown") at = Math.max(0, Math.min(shown.length - 1, at + 1));
    else if (e.key === "ArrowUp") at = Math.max(0, at - 1);
    else return;
    e.preventDefault();
  }

  function down(e: MouseEvent, it: PaletteItem) {
    e.preventDefault();
    pick(it);
  }

  onMount(() => input.focus());
</script>

<Shade dismiss={close}>
  <div class="pal" role="dialog" aria-label={palette.title}>
    <div class="pal-head">{palette.title}</div>
    <input bind:this={input} type="text" role="combobox" aria-label={palette.title} aria-controls="pal-list" aria-expanded="true" aria-autocomplete="list"
      aria-activedescendant={shown.length ? `pal-opt-${at}` : undefined} placeholder={t("{title}: type to narrow", { title: palette.title })} autocomplete="off"
      spellcheck="false" oninput={typed} onkeydown={key} />
    <ol id="pal-list" role="listbox" aria-label={palette.title}>
      {#each shown as it, i (it)}
        <li id={`pal-opt-${i}`} role="option" aria-selected={String(i === at) as "true" | "false"} onmousedown={(e) => down(e, it)}>
          <span>{it.label}</span>{#if it.meta}<span class="pill">{it.meta}</span>{/if}{#if it.key}<kbd>{it.key}</kbd>{/if}
        </li>
      {/each}
    </ol>
    <p class="pal-empty" aria-live="polite">{shown.length ? "" : palette.empty || t("Nothing matches")}</p>
  </div>
</Shade>
