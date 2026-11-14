<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { planDoes } from "./gates";

  // What applying a plan item does, step by step, and that it posts nothing when no step may post.
  let { item }: { item: J } = $props();
  const does = $derived(planDoes(item));
</script>

{#if does}
  <div class="plan-does">
    <ul>
      {#each does.steps as st, i (i)}
        <li class="plan-do">
          <span class="plan-do-what" title={st.use}>{st.text}</span>
          {#if st.items.length}<ul class="plan-do-items">{#each st.items as x, j (j)}<li class="plan-do-item">{x}</li>{/each}</ul>{/if}
        </li>
      {/each}
    </ul>
    {#if does.postsNothing}<p class="meta plan-posts">{t("Posts nothing")}</p>{/if}
  </div>
{/if}
