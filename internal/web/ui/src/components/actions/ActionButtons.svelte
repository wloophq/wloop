<script lang="ts">
  import { actionClass, actionStates, actionTitle, focusSlot } from "../../lib/actions";
  import { keyOf, type J } from "../../lib/util";
  import { act } from "../../state/actions.svelte";
  import { busy } from "../../state/store.svelte";
  import ActionButton from "../common/ActionButton.svelte";

  // The served actions of a row, candidate or detail on a surface; the question card, intake and brief groups
  // draw themselves. scope prefixes each button's slot, run replaces act.
  let {
    subject,
    surface,
    scope = undefined,
    groups = undefined,
    rove = false,
    run = undefined,
  }: { subject: J; surface: string; scope?: string; groups?: string[]; rove?: boolean; run?: (id: string) => unknown } = $props();

  const key = $derived(keyOf(subject));
  const states = $derived(actionStates(subject, surface, groups));
</script>

{#each states as [st, sp] (st.id)}
  <ActionButton
    slot={`${scope || "row:" + key}:${focusSlot(st.id)}`}
    class={actionClass(st)}
    title={actionTitle(st, sp)}
    disabled={busy.has(key) || !st.enabled}
    {rove}
    stop
    run={() => (run ? run(st.id) : act(subject, st.id))}>{st.label || sp.label}</ActionButton>
{/each}
