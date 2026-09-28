<script lang="ts">
  import * as api from '../api'
  import type { Loop } from '../api'
  import { store, run } from '../state.svelte'
  const s = $derived(store.state)
  let text = $state('coffee is ready')
  let level = $state('info')
  let ttl = $state(30)
  const num = (e: Event) => Number((e.target as HTMLInputElement | HTMLSelectElement).value)
  const str = (e: Event) => (e.target as HTMLSelectElement).value
  // meter maps -90..0 dB onto 0..100%
  const pct = (db: number) => Math.max(0, Math.min(100, (db + 90) / 0.9))
  let np = $state({ name: '', kind: 'virtual' as 'virtual' | 'serial', address: '', baud: 921600, w: 64, h: 32, fps: 30 })
  async function addPanel() {
    const name = np.name.trim()
    if (s?.panels.some((p) => p.name === name) && !confirm(`Replace panel ${name}?`)) return
    const spec = np.kind === 'serial'
      ? { kind: np.kind, address: np.address, baud: np.baud, fps: np.fps }
      : { kind: np.kind, w: np.w, h: np.h, fps: np.fps }
    if (await run(() => api.putPanel(np.name, spec))) np.name = ''
  }
</script>

{#if s && store.options}
  <h2>Loops</h2>
  {#each ([['music', s.settings.music], ['idle', s.settings.idle]] as [string, Loop][]) as [name, loop]}
    <div class="card">
      <label>{name} loop: {loop.loop} s (0 = stay)
        <input type="range" min="0" max="300" step="5" value={loop.loop}
          onchange={(e) => run(() => api.patchController({ [name]: { loop: num(e) } }))} />
      </label>
      <select value={loop.order} onchange={(e) => run(() => api.patchController({ [name]: { order: str(e) } }))}>
        {#each store.options.orders as o}<option value={o}>{o}</option>{/each}
      </select>
    </div>
  {/each}

  <h2>Music detection</h2>
  <div class="card">
    <div class="meter">
      <div class="level" style="width:{pct(s.db)}%"></div>
      <div class="mark" style="left:{pct(s.settings.silence_db)}%"></div>
      <div class="mark" style="left:{pct(s.settings.music_db)}%"></div>
    </div>
    <div class="row muted"><span>{s.db.toFixed(1)} dB</span><span>{s.playing ? 'playing' : 'silent'}</span><span>{s.bpm ? s.bpm.toFixed(0) + ' bpm' : ''}</span></div>
    <label>music above {s.settings.music_db} dB
      <input type="range" min="-90" max="0" value={s.settings.music_db} onchange={(e) => run(() => api.patchController({ music_db: num(e) }))} /></label>
    <label>silence below {s.settings.silence_db} dB
      <input type="range" min="-90" max="0" value={s.settings.silence_db} onchange={(e) => run(() => api.patchController({ silence_db: num(e) }))} /></label>
    <label>silence after {s.settings.silence_after} s
      <input type="range" min="0" max="60" value={s.settings.silence_after} onchange={(e) => run(() => api.patchController({ silence_after: num(e) }))} /></label>
  </div>

  <h2>Events</h2>
  <div class="card">
    <input type="text" bind:value={text} />
    <div class="row">
      <select bind:value={level}>{#each store.options.levels as l}<option value={l}>{l}</option>{/each}</select>
      <input type="number" bind:value={ttl} min="1" max="3600" />
      <button class="primary" onclick={() => run(() => api.postEvent(text, level, ttl))}>Send</button>
    </div>
    {#each s.events as ev}
      <div class="row">
        <!-- svelte-ignore a11y_label_has_associated_control -->
        <label>{ev.level}: {ev.text}</label>
        <button class="danger" onclick={() => run(() => api.deleteEvent(ev.id))}>✕</button>
      </div>
    {/each}
  </div>

  <h2>Panels</h2>
  {#each s.panels as p}
    <div class="card">
      <div class="row"><strong>{p.name}</strong><span class="muted">{p.kind}{p.w ? ` · ${p.w}x${p.h}` : ''}{p.kind === 'serial' ? (p.connected ? ' · connected' : ' · connecting') : ''}{p.kind === 'virtual' ? ` · ${p.clients} viewer${p.clients === 1 ? '' : 's'}` : ''}</span></div>
      {#if p.kind !== 'terminal'}
        <label>fps {p.fps}
          <input type="range" min="1" max="120" value={p.fps} onchange={(e) => run(() => api.patchPanel(p.name, { fps: num(e) }))} /></label>
        {#if p.kind === 'serial'}
          <label>brightness {p.brightness ?? 0}
            <input type="range" min="0" max="255" value={p.brightness ?? 0} onchange={(e) => run(() => api.patchPanel(p.name, { brightness: num(e) }))} /></label>
          <label>idle brightness {p.idle_brightness ?? 0}
            <input type="range" min="0" max="255" value={p.idle_brightness ?? 0} onchange={(e) => run(() => api.patchPanel(p.name, { idle_brightness: num(e) }))} /></label>
        {/if}
        {#if p.status}<div class="muted">fps {p.status.fps} · frames {p.status.frames_ok} · crc errors {p.status.crc_err} · gaps {p.status.seq_gaps}{p.status.temp_c !== 255 ? ` · ${p.status.temp_c} °C` : ''}</div>{/if}
        <button class="danger" onclick={() => { if (confirm(`Delete panel ${p.name}?`)) run(() => api.deletePanel(p.name)) }}>Delete</button>
      {:else}
        <div class="muted">fps {p.fps} · follows the window</div>
      {/if}
    </div>
  {/each}
  <div class="card">
    <input type="text" placeholder="name" bind:value={np.name} />
    <div class="row">
      <select bind:value={np.kind}><option value="virtual">virtual</option><option value="serial">serial</option></select>
      <input type="number" bind:value={np.fps} min="1" max="120" /> fps
    </div>
    {#if np.kind === 'serial'}
      <div class="row"><input type="text" placeholder="/dev/ttyUSB0 or tcp://host:7090" bind:value={np.address} /><input type="number" bind:value={np.baud} min="1" /></div>
    {:else}
      <div class="row"><input type="number" bind:value={np.w} min="1" max="1024" /> × <input type="number" bind:value={np.h} min="1" max="1024" /></div>
    {/if}
    <button class="primary" onclick={addPanel} disabled={!np.name}>Add panel</button>
  </div>
{/if}

<style>
  .meter { position: relative; height: 12px; background: #000; border-radius: 6px; overflow: hidden; margin: 8px 0; }
  .level { height: 100%; background: var(--accent); }
  .mark { position: absolute; top: 0; bottom: 0; width: 2px; background: var(--danger); }
</style>
