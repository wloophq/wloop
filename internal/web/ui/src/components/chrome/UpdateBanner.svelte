<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import { page, update } from "../../state/build.svelte";
</script>

<div id="update" class="update" role="status" hidden={!update.shown}>
  <span>{t("A new version is running.")}</span>
  <button id="update-reload" type="button" class="act" data-focus="update:reload" onclick={() => page.reload()}>{t("Reload")}</button>
</div>
