<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import { nav } from "../../state/route.svelte";
  import IconButton from "./IconButton.svelte";
  import { copyViewLink, detachView, detailOnly } from "./window.svelte";

  // The detail's own window controls: detach, unless it is alone already, and copy its link.
</script>

<span class="d-window"
  >{#if !detailOnly(nav.hash)}<IconButton icon="detach" label={t("Open in a window of its own")} slot="detail:detach" action="detail:detach" run={detachView} />{/if}<IconButton
    icon="link"
    label={t("Copy a link to this view")}
    slot="detail:copy-link"
    action="link:copy"
    run={copyViewLink} /></span
>
