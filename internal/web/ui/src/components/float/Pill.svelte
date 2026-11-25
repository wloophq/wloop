<script lang="ts">
  import { fmtNumber, t, tn } from "../../lib/i18n.svelte";
  import { age } from "../../lib/lens";
  import { clock } from "../../state/clock.svelte";
  import type { FloatState } from "../../state/float.svelte";
  import { floatDrag, LATE_MS, oldest, progress } from "./float";
  import Peek from "./Peek.svelte";
  import Ring from "./Ring.svelte";

  // The collapsed widget: how many decisions wait on the user, how many turns run, how old the oldest decision is
  // and, while a peek shows on a pill that is not docked, what just arrived; a dot when nothing waits and nothing runs. A drag moves the window,
  // a click toggles the panel, an Option-click hides the widget until something new.
  let { f, onenter, ontoggle, ondrop }: { f: FloatState; onenter: () => void; ontoggle: () => void; ondrop: () => void } = $props();

  // DRAG_PX is how far the pointer moves before a press is a drag.
  const DRAG_PX = 4;

  const n = $derived(f.items.length);
  const running = $derived((f.turns && f.turns.running) || []);
  const first = $derived(oldest(f.items));
  const late = $derived(!Number.isNaN(first) && clock.now - first > LATE_MS);
  const ring = $derived.by(() => {
    const usual = (f.turns && f.turns.usual) || {};
    const fr = running.map((r: { intent: string; started: string }) => progress(clock.now - Date.parse(r.started), usual[r.intent] || 0));
    return fr.length ? Math.max(...fr) : 0;
  });
  const peeked = $derived(f.peeked);
  const idle = $derived(f.online && f.loaded && !n && !running.length && f.mode !== "panel");

  let press: { x: number; y: number } | null = null;

  function down(e: PointerEvent): void {
    if (e.button === 0) press = { x: e.clientX, y: e.clientY };
  }

  function moved(e: PointerEvent): void {
    if (!press || Math.hypot(e.clientX - press.x, e.clientY - press.y) <= DRAG_PX) return;
    press = null;
    f.setMode("pill");
    floatDrag(ondrop);
  }

  function up(e: PointerEvent): void {
    if (!press) return;
    press = null;
    if (e.altKey) f.hide("quiet");
    else if (peeked) f.setMode("panel");
    else ontoggle();
  }

  function key(e: KeyboardEvent): void {
    if (e.key !== "Enter" && e.key !== " ") return;
    e.preventDefault();
    e.stopPropagation();
    ontoggle();
  }
</script>

<div
  class={["fl-pill", idle && "dot", peeked && "peeking", !f.online && "offline"]}
  role="button"
  tabindex="0"
  aria-expanded={f.mode === "panel" ? "true" : "false"}
  aria-label={f.read === "offline" ? t("Offline") : f.read === "failed" ? t("Could not read the queue.") : tn("{n} wait on you", n)}
  onpointerenter={onenter}
  onpointerdown={down}
  onpointermove={moved}
  onpointerup={up}
  onkeydown={key}
>
  {#if idle}
    <i class="fl-dot tone-muted" aria-hidden="true"></i>
  {:else}
    <i class="fl-mark" aria-hidden="true"></i>
    {#if f.read === "offline"}
      <span class="fl-offline">{t("Offline")}</span>
    {:else if f.read === "failed"}
      <span class="fl-offline">{t("Queue unavailable")}</span>
    {:else}
      <span class="fl-count"><i class="fl-dot" aria-hidden="true"></i>{fmtNumber(n)}</span>
      {#if running.length}<span class="fl-running"><Ring value={ring} />{fmtNumber(running.length)}</span>{/if}
      {#if n && !Number.isNaN(first)}
        <span class={["fl-age", late && "late"]}>{age(new Date(first).toISOString(), clock.now)}</span>
      {/if}
      {#if peeked && !f.place.edge}
        <span class="fl-peek"><Peek item={peeked} /></span>
      {/if}
    {/if}
  {/if}
</div>
