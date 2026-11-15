<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { viewRule } from "./views";

  // A view's summary under its heading, and the rule its profile lists it by behind a toggle.
  let { spec }: { spec: J } = $props();
  const rule = $derived(viewRule(spec));
</script>

{#if spec && spec.summary}
  <div class="view-intro">
    <p class="view-summary">{spec.summary}</p>
    {#if rule.length}
      <details class="view-rule">
        <summary>{t("Why these tasks?")}</summary>
        <dl>
          {#each rule as [k, v] (k)}<dt>{k}</dt><dd><code>{v}</code></dd>{/each}
        </dl>
      </details>
    {/if}
  </div>
{/if}
