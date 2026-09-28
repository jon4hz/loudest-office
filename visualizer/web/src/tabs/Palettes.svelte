<script lang="ts">
  import Gradient from '../lib/Gradient.svelte'
  import * as api from '../api'
  import type { Custom, PaletteInfo } from '../api'
  import { store, run } from '../state.svelte'
  let editing = $state<Custom | null>(null)
  let name = $state('')
  let isNew = $state(true)

  function edit(p: PaletteInfo) {
    name = p.name
    isNew = false
    editing = $state.snapshot(p.custom!) as Custom
  }
  // clone opens the editor prefilled: a custom palette with its stops, a
  // shipped one with 8 stops sampled evenly from its preview
  function clone(p: PaletteInfo) {
    name = p.name + ' copy'
    isNew = true
    editing = p.custom ? $state.snapshot(p.custom) as Custom : {
      axis: 'bands', peak: '#ffffff',
      stops: Array.from({ length: 8 }, (_, i) => ({ pos: i / 7, color: p.preview[Math.round((i / 7) * (p.preview.length - 1))] })),
    }
  }
  function fresh() {
    name = ''
    isNew = true
    editing = { axis: 'bands', peak: '#ffffff', stops: [{ pos: 0, color: '#000080' }, { pos: 1, color: '#ffffff' }] }
  }
  function sorted(): Custom {
    const e = editing!
    const stops = [...e.stops].sort((a, b) => a.pos - b.pos)
    stops[0].pos = 0
    stops[stops.length - 1].pos = 1
    return { ...e, stops }
  }
  async function save() {
    if (!name.trim()) { store.error = 'name is required'; return }
    if (await run(() => api.putPalette(name.trim(), sorted()), 'all')) editing = null
  }
  async function remove(n: string) {
    if (!confirm(`Delete palette "${n}"?`)) return
    if (await run(() => api.deletePalette(n), 'all')) editing = null
  }
</script>

{#if editing}
  <div class="card">
    <input type="text" placeholder="name" bind:value={name} disabled={!isNew} />
    <Gradient stops={[...editing.stops].sort((a, b) => a.pos - b.pos)} />
    {#each editing.stops as stop, i}
      <div class="row">
        <input type="color" bind:value={stop.color} />
        <input type="range" min="0" max="1" step="0.01" bind:value={stop.pos} />
        <span class="muted">{stop.pos.toFixed(2)}</span>
        <button class="danger" disabled={editing.stops.length <= 2} onclick={() => editing!.stops.splice(i, 1)}>✕</button>
      </div>
    {/each}
    <div class="row">
      <button disabled={editing.stops.length >= 16} onclick={() => editing!.stops.push({ pos: 0.5, color: '#ff0000' })}>+ stop</button>
      <label>axis <select bind:value={editing.axis}><option value="bands">across bands</option><option value="height">up each bar</option></select></label>
      <label>peak <input type="color" bind:value={editing.peak} /></label>
    </div>
    <div class="row">
      <button class="primary" onclick={save}>Save</button>
      <button onclick={() => (editing = null)}>Cancel</button>
      {#if !isNew}<button class="danger" onclick={() => remove(name)}>Delete</button>{/if}
    </div>
  </div>
{:else}
  <button class="primary" onclick={fresh}>New palette</button>
{/if}

{#each store.palettes as p}
  <div class="card">
    <div class="row">
      <!-- svelte-ignore a11y_label_has_associated_control -->
      <label>{p.name} {#if !p.custom}<span class="muted">shipped</span>{/if}</label>
      {#if p.custom}<button onclick={() => edit(p)}>Edit</button>{/if}
      <button onclick={() => clone(p)}>Clone</button>
    </div>
    <Gradient colors={p.preview} />
  </div>
{/each}
