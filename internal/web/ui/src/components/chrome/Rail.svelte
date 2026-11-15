<script lang="ts">
  import { brand } from "../../brand";
  import { fmtNumber, t } from "../../lib/i18n.svelte";
  import { lensOf } from "../../lib/lens";
  import { readHash } from "../../state/route.svelte";
  import { keyCap } from "../../state/keycaps.svelte";
  import { homeItem, railLayout, railNav, railPeople } from "./rail";
  import RailItem from "./RailItem.svelte";

  // The rail: the omnibox entry, the views by section with the empty ones folded into one entry, and whom the
  // rows shown wait on. omnibox opens the omnibox.
  let { omnibox }: { omnibox: () => void } = $props();

  let emptiesOpen = $state(false);
  const route = $derived(readHash());
  const lens = $derived(lensOf(route));
  const layout = $derived(railLayout(railNav(lens, route), route));
  const people = $derived(railPeople(lens, route));
  const key = $derived(keyCap("palette"));
</script>

<nav id="rail" aria-label={t("Views")}>
  <div class="rail-mark"><span class="mark">{brand.name}</span></div>
  <button id="omnibox-entry" type="button" class="find" aria-keyshortcuts="Meta+K" data-focus="rail:omnibox" onclick={() => omnibox()}
    ><span class="label">{t("Search or ask")}</span>{#if key}<kbd>{key}</kbd>{/if}</button>
  <ul id="views" class="views">
    <RailItem item={homeItem(route)} />
    {#each layout.sections as sec, i (sec.title + ":" + i)}
      {#if sec.title}<li class="rail-head" role="presentation">{sec.title}</li>{/if}
      {#each sec.items as item (item.id)}<RailItem {item} />{/each}
    {/each}
    {#if layout.empties.length}
      <li>
        <button type="button" class="nav rail-empties" aria-expanded={(emptiesOpen) ? "true" : "false"} data-focus="rail:empties" onclick={() => (emptiesOpen = !emptiesOpen)}
          ><span class="label">{t("Empty")}</span><span class="count">{fmtNumber(layout.empties.length)}</span></button>
      </li>
      {#if emptiesOpen}
        {#each layout.empties as item (item.id)}<RailItem {item} />{/each}
      {/if}
    {/if}
  </ul>
  <h2 class="rail-head">{t("Waiting on")}</h2>
  <ul id="people" class="views people">
    {#each people as item (item.id)}<RailItem {item} />{/each}
  </ul>
</nav>
