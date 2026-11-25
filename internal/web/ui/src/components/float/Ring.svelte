<script lang="ts">
  // A running turn's progress as a static arc that grows from the top with value, from 0 to 1.
  let { value = 0 }: { value?: number } = $props();

  const R = 5;
  const C = 2 * Math.PI * R;
</script>

<svg class="fl-ring" viewBox="0 0 14 14" width="14" height="14" aria-hidden="true">
  <circle class="track" cx="7" cy="7" r={R} />
  <circle class="arc" cx="7" cy="7" r={R} stroke-dasharray={`${Math.max(value, 0.08) * C} ${C}`} transform="rotate(-90 7 7)" />
</svg>
