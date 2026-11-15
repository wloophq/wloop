<script lang="ts">
import { verbState } from "../../state/verbs";
  import type { Snippet } from "svelte";
  import type { J } from "../../lib/util";
  import ActionButton from "../common/ActionButton.svelte";
  import { verbClass, verbSlot, verbTitle } from "./task";

  // The button of a verb e offers, off with its reason when the verb is off; none when e does not offer it.
  let {
    e,
    verb,
    run,
    scope = "detail",
    primary = false,
    name = undefined,
    children,
  }: { e: J; verb: string; run: () => unknown; scope?: string; primary?: boolean; name?: string; children: Snippet } = $props();

  const st = $derived(verbState(e, verb));
</script>

{#if st}
  <ActionButton slot={verbSlot(e, verb, scope)} class={verbClass(st, primary)} disabled={!st.enabled} title={verbTitle(e, verb)} label={name} {run}>{@render children()}</ActionButton>
{/if}
