<script lang="ts">
  import * as api from '../api'
  import { store, run } from '../state.svelte'
  const active = $derived(store.state?.preset ?? '')
  const modified = $derived(store.state?.modified ?? false)
  function saveAs() {
    const name = prompt('Preset name')?.trim()
    if (!name) return
    if (store.presets.includes(name) && !confirm(`Overwrite preset "${name}"?`)) return
    run(() => api.savePreset(name), 'all')
  }
  function remove(name: string) {
    if (confirm(`Delete preset "${name}"?`)) run(() => api.deletePreset(name), 'all')
  }
</script>

<div class="row">
  <button class="primary" onclick={saveAs}>Save current as…</button>
  {#if active}<button onclick={() => run(() => api.savePreset(active), 'all')}>Overwrite "{active}"</button>{/if}
</div>
{#each store.presets as name}
  <div class="card row">
    <!-- svelte-ignore a11y_label_has_associated_control -->
    <label><strong>{name}</strong>{#if name === active}<span class="muted"> · active{#if modified} (modified){/if}</span>{/if}</label>
    <button onclick={() => run(() => api.loadPreset(name), 'all')}>Load</button>
    <button class="danger" onclick={() => remove(name)}>✕</button>
  </div>
{/each}
{#if store.presets.length === 0}<div class="muted">No presets yet. Set things up, then save the current state.</div>{/if}
