<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import { safeURL, type J } from "../../lib/util";

  // What the task's asks say, each with who asked, its source and the quotes it was read from.
  let { e }: { e: J } = $props();
</script>

{#if (e.asks || []).length}
  <section class="d-asks" aria-label={t("Asks")}>
    <ul class="asks">
      {#each e.asks as a, i (a.item || i)}
        <li class="ask-item">
          <div>{#if a.asker}<strong>{a.asker + ": "}</strong>{/if}{a.summary}{#if safeURL(a.permalink)}{" "}<a href={safeURL(a.permalink)} target="_blank" rel="noopener noreferrer" class="ext">{t("source")}</a>{/if}</div>
          {#each a.quotes || [] as q, j (j)}<blockquote class="quote">{q}</blockquote>{/each}
        </li>
      {/each}
    </ul>
  </section>
{/if}
