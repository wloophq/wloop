<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import { nav } from "../../state/route.svelte";
  import IconButton from "./IconButton.svelte";
  import { copyViewLink, page, windowTitle } from "./window.svelte";

  // The title bar the page draws when the native window draws its controls over it: part start is the history
  // buttons and the title, which go before the top bar's own controls, part end the link and the omnibox, which go
  // after them.
  let { part, omnibox }: { part: "start" | "end"; omnibox: () => void } = $props();

  // The history moves with the route; the Navigation API says nothing when it does.
  const can = $derived.by(() => {
    void nav.hash;
    return page.canGo();
  });
</script>

{#if part === "start"}
  <span class="tb-nav"
    ><IconButton icon="back" label={t("Back")} slot="top:back" run={page.back} disabled={!!can && !can.back} /><IconButton
      icon="forward"
      label={t("Forward")}
      slot="top:forward"
      run={page.forward}
      disabled={!!can && !can.forward} /></span
  ><span id="window-title" class="tb-title">{windowTitle()}</span>
{:else}
  <span class="tb-tools"
    ><IconButton icon="link" label={t("Copy a link to this view")} slot="top:copy-link" action="link:copy" run={copyViewLink} /><IconButton
      icon="search"
      label={t("Open the omnibox")}
      slot="top:omnibox"
      run={omnibox} /></span
  >
{/if}
