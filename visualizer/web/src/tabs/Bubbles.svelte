<script lang="ts">
  import Form from '../lib/Form.svelte'
  import * as api from '../api'
  import { store, run } from '../state.svelte'
  let open = $state('')
  const pinned = $derived(store.state?.settings.show ?? '')
  const choices = $derived(store.options ? {
    palettes: store.options.palettes, layouts: store.options.layouts, peaks: store.options.peaks,
    levels: store.options.levels, orders: store.options.orders,
  } : {} as Record<string, string[]>)
</script>

{#each store.bubbles as b}
  <div class="card">
    <div class="row">
      <!-- svelte-ignore a11y_label_has_associated_control -->
      <label><strong>{b.name}</strong> <span class="muted">{b.kind}</span></label>
      {#if b.kind !== 'event'}
        <button class={pinned === b.name ? 'primary' : ''}
          onclick={() => run(() => api.patchController({ show: pinned === b.name ? '' : b.name }))}>Pin</button>
      {/if}
      {#if b.kind !== 'event'}
        <button disabled={pinned !== ''} title={pinned ? 'unpin first: a pin means only that bubble' : ''}
          onclick={() => run(() => api.show(b.name, 10))}>10 s</button>
      {/if}
      <button onclick={() => (open = open === b.name ? '' : b.name)}>{open === b.name ? '▾' : '▸'}</button>
    </div>
    {#if b.kind === 'music' || b.kind === 'idle'}
      <label>Weight {b.weight === 0 ? 'off' : b.weight}
        <input type="range" min="0" max="10" value={b.weight}
          onchange={(e) => run(() => api.patchBubble(b.name, { weight: Number((e.target as HTMLInputElement).value) }), 'all')} />
      </label>
    {:else}
      <label><input type="checkbox" checked={b.weight > 0}
        onchange={(e) => run(() => api.patchBubble(b.name, { weight: (e.target as HTMLInputElement).checked ? 1 : 0 }), 'all')} /> enabled</label>
    {/if}
    {#if open === b.name && store.options}
      <Form settings={b.settings} hints={store.options.bubbles[b.name] ?? {}} {choices}
        onpatch={(patch) => run(() => api.patchBubble(b.name, { settings: patch }), 'all')} />
    {/if}
  </div>
{/each}
