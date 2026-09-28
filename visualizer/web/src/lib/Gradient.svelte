<script lang="ts">
  import type { Stop } from '../api'
  let { stops, colors }: { stops?: Stop[]; colors?: string[] } = $props()
  const css = $derived(stops
    ? stops.map((s) => `${s.color} ${(s.pos * 100).toFixed(1)}%`).join(', ')
    : (colors ?? []).join(', '))
</script>

<div class="strip" style="background: linear-gradient(to right, {css})"></div>

<style>
  .strip { height: 24px; border-radius: 6px; }
</style>
