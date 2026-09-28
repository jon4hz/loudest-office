<script lang="ts">
  import { onMount } from 'svelte'
  import './app.css'
  import { store, loadAll, startPolling } from './state.svelte'
  import Now from './tabs/Now.svelte'
  import Bubbles from './tabs/Bubbles.svelte'
  import Palettes from './tabs/Palettes.svelte'
  import Presets from './tabs/Presets.svelte'
  import Setup from './tabs/Setup.svelte'
  const tabs = ['Now', 'Bubbles', 'Palettes', 'Presets', 'Setup'] as const
  let tab = $state<(typeof tabs)[number]>('Now')
  onMount(() => { loadAll(); return startPolling() })
</script>

<main>
  {#if tab === 'Now'}<Now />{/if}
  {#if tab === 'Bubbles'}<Bubbles />{/if}
  {#if tab === 'Palettes'}<Palettes />{/if}
  {#if tab === 'Presets'}<Presets />{/if}
  {#if tab === 'Setup'}<Setup />{/if}
</main>
{#if store.error}
  <!-- a toast above the nav, not a banner at the top: a phone scrolled down
       a long tab must still see why a tap did nothing -->
  <button class="toast error" onclick={() => (store.error = '')}>{store.error}</button>
{/if}
<nav>
  {#each tabs as t}
    <button class={tab === t ? 'on' : ''} onclick={() => { tab = t; loadAll() }}>{t}</button>
  {/each}
</nav>
