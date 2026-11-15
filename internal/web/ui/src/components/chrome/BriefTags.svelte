<script lang="ts">
  import { t, tn } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { RISKS, SIZES } from "./facts";

  // The pills of an Explore candidate's brief summary; open_questions is a count.
  let { b }: { b: J } = $props();
  const TAG_CHIPS = 3;
  const size = $derived(SIZES.includes(b.size) ? b.size : "");
  const risk = $derived(RISKS.includes(b.risk) ? b.risk : "");
  const n = $derived(Number.isInteger(b.open_questions) && b.open_questions > 0 ? b.open_questions : 0);
  const tags = $derived((Array.isArray(b.tags) ? b.tags : []) as string[]);
  const type = $derived(b.kind || "");
  const rest = $derived(tags.slice(TAG_CHIPS));
</script>

{#if type || size || risk || n || tags.length}
  <span class="btags"
    >{#if type}<span class="pill" title={t("type {type}", { type })}>{type}</span>{/if}{#if size}<span class="pill" title={t("size {size}", { size })}
        >{size.toUpperCase()}</span
      >{/if}{#if risk}<span class={["pill", `risk-${risk}`].join(" ")}>{t("risk {risk}", { risk })}</span>{/if}{#if n}<span class="pill qs" title={tn("{n} open questions in the brief", n)}
        >{`? ${n}`}</span
      >{/if}{#each tags.slice(0, TAG_CHIPS) as tag (tag)}<span class="pill tag">{tag}</span>{/each}{#if rest.length}<span class="pill tag" title={rest.join(", ")}
        >{`+${rest.length}`}</span
      >{/if}</span
  >
{/if}
