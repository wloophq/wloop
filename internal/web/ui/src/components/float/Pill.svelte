<script lang="ts">
  import { fmtNumber, t, tn } from "../../lib/i18n.svelte";
  import { clock } from "../../state/clock.svelte";
  import type { FloatState } from "../../state/float.svelte";
  import { floatDrag, progress } from "./float";
  import Ring from "./Ring.svelte";

  // The tab: a circle with how many decisions wait on the user, and beside it, while turns run, a second circle with
  // their ring and count; a dot when nothing waits and nothing runs, a muted or failed dot when the queue is unread.
  // A drag moves the window, a click toggles the panel, an Option-click hides the widget until something new.
  let {
    f,
    onenter,
    onleave,
    ontoggle,
    ondrop,
  }: { f: FloatState; onenter: () => void; onleave: () => void; ontoggle: () => void; ondrop: () => void } = $props();

  // DRAG_PX is how far the pointer moves before a press is a drag.
  const DRAG_PX = 4;

  const n = $derived(f.items.length);
  const running = $derived((f.turns && f.turns.running) || []);
  const ring = $derived.by(() => {
    const usual = (f.turns && f.turns.usual) || {};
    const fr = running.map((r: { intent: string; started: string }) => progress(clock.now - Date.parse(r.started), usual[r.intent] || 0));
    return fr.length ? Math.max(...fr) : 0;
  });
  const idle = $derived(f.online && f.loaded && !n && !running.length);

  let press: { x: number; y: number } | null = null;

  function down(e: PointerEvent): void {
    if (e.button === 0) press = { x: e.clientX, y: e.clientY };
  }

  function moved(e: PointerEvent): void {
    if (!press || Math.hypot(e.clientX - press.x, e.clientY - press.y) <= DRAG_PX) return;
    press = null;
    onleave();
    floatDrag(ondrop);
  }

  function up(e: PointerEvent): void {
    if (!press) return;
    press = null;
    if (e.altKey) f.hide("quiet");
    else ontoggle();
  }
</script>

<div
  class={["fl-pill", idle && "dot", !f.online && "offline"]}
  role="button"
  tabindex="-1"
  aria-label={f.read === "offline" ? t("Offline") : f.read === "failed" ? t("Could not read the queue.") : tn("{n} wait on you", n)}
  onpointerenter={onenter}
  onpointerleave={onleave}
  onpointerdown={down}
  onpointermove={moved}
  onpointerup={up}
>
  {#if idle}
    <i class="fl-dot tone-muted" aria-hidden="true"></i>
  {:else}
    {#if f.read === "offline" || f.read === "failed"}
      <span class="fl-slot"><i class={["fl-dot", f.read === "failed" ? "tone-err" : "tone-muted"]} aria-hidden="true"></i></span>
    {:else}
      <span class="fl-slot fl-count">{fmtNumber(n)}</span>
      {#if running.length}<span class="fl-slot fl-running"><Ring value={ring} size={26} /><b>{fmtNumber(running.length)}</b></span>{/if}
    {/if}
  {/if}
</div>
