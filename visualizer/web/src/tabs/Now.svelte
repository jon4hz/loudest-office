<script lang="ts">
  import Preview from '../lib/Preview.svelte'
  import * as api from '../api'
  import { store, run } from '../state.svelte'
  const s = $derived(store.state)
  const looping = $derived((s?.settings.music.loop ?? 0) > 0)
  const panels = $derived(s?.panels ?? [])
  let picked = $state('')
  try { picked = localStorage.getItem('panel') ?? '' } catch { /* private window */ }
  const shown = $derived(panels.some((p) => p.name === picked) ? picked : (panels[0]?.name ?? ''))
  function pick(e: Event) {
    picked = (e.target as HTMLSelectElement).value
    try { localStorage.setItem('panel', picked) } catch { /* ignore */ }
  }
  function toggleLoop() {
    if (!s) return
    let loop = 0
    if (looping) {
      localStorage.setItem('loop', String(s.settings.music.loop))
    } else {
      loop = Number(localStorage.getItem('loop')) || 30
    }
    run(() => api.patchController({ music: { loop } }))
  }
</script>

{#if panels.length > 1}
  <select value={shown} onchange={pick}>{#each panels as p}<option value={p.name}>{p.name}</option>{/each}</select>
{/if}
{#if shown}<Preview name={shown} />{/if}
{#if s}
  <div class="card">
    <div class="row"><strong>{s.active || 'blank'}</strong><span class="muted">{s.kind}{s.playing ? ' · playing' : ''}</span></div>
    <div class="row muted">preset: {s.preset ? s.preset + (s.modified ? ' (modified)' : '') : 'none'}</div>
  </div>
  {#each panels.filter((p) => p.kind === 'serial') as p}
    <div class="card">
      <label>{p.name} brightness {p.brightness ?? 0}
        <input type="range" min="0" max="255" value={p.brightness ?? 0}
          onchange={(e) => run(() => api.patchPanel(p.name, { brightness: Number((e.target as HTMLInputElement).value) }))} />
      </label>
    </div>
  {/each}
  <div class="row">
    <button onclick={() => run(() => api.next())}>Next</button>
    <button class={looping ? 'primary' : ''} onclick={toggleLoop}>Auto loop {looping ? 'on' : 'off'}</button>
  </div>
{/if}
