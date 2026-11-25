<script lang="ts">
  import { fmtNumber, t, tn } from "../../lib/i18n.svelte";
  import { age } from "../../lib/lens";
  import { clock } from "../../state/clock.svelte";
  import type { FloatState } from "../../state/float.svelte";
  import { floatDrag, itemRef, itemText, LATE_MS, oldest, progress } from "./float";
  import Ring from "./Ring.svelte";

  // The collapsed widget: how many decisions wait on the user, how many turns run, how old the oldest decision is
  // and, while a peek shows, what just arrived. A drag moves the window; a click toggles the panel.
  let { f, onenter, ontoggle }: { f: FloatState; onenter: () => void; ontoggle: () => void } = $props();

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
  const idle = $derived(f.online && !n && !running.length);

  let press: { x: number; y: number } | null = null;

  function down(e: PointerEvent): void {
    if (e.button === 0) press = { x: e.clientX, y: e.clientY };
  }

  function moved(e: PointerEvent): void {
    if (!press || Math.hypot(e.clientX - press.x, e.clientY - press.y) <= DRAG_PX) return;
    press = null;
    f.setMode("pill");
    floatDrag();
  }

  function up(): void {
    if (!press) return;
    press = null;
    if (peeked) f.setMode("panel");
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
  class={["fl-pill", idle && "idle", peeked && "peeking", !f.online && "offline"]}
  role="button"
  tabindex="0"
  aria-expanded={f.mode === "panel" ? "true" : "false"}
  aria-label={f.online ? tn("{n} wait on you", n) : t("Offline")}
  onpointerenter={onenter}
  onpointerdown={down}
  onpointermove={moved}
  onpointerup={up}
  onkeydown={key}
>
  <i class="fl-mark" aria-hidden="true"></i>
  {#if !f.online}
    <span class="fl-offline">{t("Offline")}</span>
  {:else}
    <span class="fl-count"><i class="fl-dot" aria-hidden="true"></i>{fmtNumber(n)}</span>
    {#if running.length}<span class="fl-running"><Ring value={ring} />{fmtNumber(running.length)}</span>{/if}
    {#if n && !Number.isNaN(first)}
      <span class={["fl-age", late && "late"]}>{age(new Date(first).toISOString(), clock.now)}</span>
    {/if}
    {#if peeked}
      <span class="fl-peek"><b>{peeked.word || peeked.kind}</b>{[itemRef(peeked), itemText(peeked)].filter(Boolean).map((s) => " · " + s).join("")}</span>
    {/if}
  {/if}
</div>
