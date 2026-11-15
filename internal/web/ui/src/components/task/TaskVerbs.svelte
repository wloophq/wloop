<script lang="ts">
import { verbLabel, verbTask } from "../../state/verbs";
  import { actionStates, focusSlot } from "../../lib/actions";
  import { fmtNumber, t } from "../../lib/i18n.svelte";
  import { keyOf, type J } from "../../lib/util";
  import ActionButtons from "../actions/ActionButtons.svelte";
  import ActionResult from "../actions/ActionResult.svelte";
  import ActionButton from "../common/ActionButton.svelte";
  import { keyCap, servedKey } from "../../state/keycaps.svelte";
  import { moreVerbs, openMore, primaryVerbs, runVerb, taskIntentAct, verbSlot } from "./task";
  import VerbButton from "./VerbButton.svelte";

  // The profile's primary verbs the task offers, More over the rest, the intents that serve it and the actions
  // its units offer on it, with the last answer of the first of them that has one.
  let { e }: { e: J } = $props();

  const primary = $derived(primaryVerbs(e));
  const rest = $derived(moreVerbs(e).length);
  const intents = $derived(e.actions || []);
  const units = $derived((e.unit_actions || []).filter((u: J) => actionStates(u, "task").length));
  const paletteKey = $derived(keyCap("palette"));
  const slots = $derived([
    ...primary.map((v) => verbSlot(e, v.verb)),
    ...(rest ? [`detail:more:${e.id}`] : []),
    ...intents.map((a: J) => `detail:${a.id}:${e.id}`),
    ...units.flatMap((u: J) => actionStates(u, "task").map(([st]) => `task:${keyOf(u)}:${focusSlot(st.id)}`)),
  ]);
</script>

{#if slots.length}
  <div class="verbs d-actions">
    {#each primary as v (v.verb)}
      {@const key = verbTask(v.verb).key}
      <VerbButton {e} verb={v.verb} run={() => runVerb(e, v.verb)}><span class="lb">{verbLabel(v.verb)}</span>{#if key}{" "}<kbd>{key}</kbd>{/if}</VerbButton>
    {/each}
    {#if rest}
      <ActionButton slot={`detail:more:${e.id}`} class="act more" keyshortcuts={servedKey("palette").replace("Cmd+", "Meta+") || undefined} run={() => openMore(e)}><span class="lb">{t("More {n}", { n: fmtNumber(rest) })}</span>{#if paletteKey}{" "}<kbd>{paletteKey}</kbd>{/if}</ActionButton>
    {/if}
    {#each intents as a (a.id)}
      <ActionButton slot={`detail:${a.id}:${e.id}`} class={"act" + (a.enabled ? "" : " off")} disabled={!a.enabled} title={a.enabled ? a.note || undefined : a.reason} run={() => taskIntentAct(e, a.id)}>{a.label || a.id}</ActionButton>
    {/each}
    {#each units as u (keyOf(u))}
      <ActionButtons subject={u} surface="task" scope={"task:" + keyOf(u)} />
    {/each}
  </div>
  <ActionResult {slots} />
{/if}
