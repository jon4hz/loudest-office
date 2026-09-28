<script lang="ts">
  import type { Hint } from '../api'
  let { settings, hints, choices, onpatch }: {
    settings: Record<string, unknown>
    hints: Record<string, Hint>
    choices: Record<string, string[]>
    onpatch: (patch: Record<string, unknown>) => void
  } = $props()
  const keys = $derived(Object.keys(hints).sort())
  function toggle(key: string, value: string) {
    const cur = (settings[key] as string[]) ?? []
    const next = cur.includes(value) ? cur.filter((v) => v !== value) : [...cur, value]
    onpatch({ [key]: next })
  }
  const val = (e: Event) => (e.target as HTMLInputElement)
</script>

{#each keys as key}
  {@const h = hints[key]}
  <div class="field">
    <div class="muted">{key}</div>
    {#if h.type === 'multi'}
      <!-- an empty list means all; the chip also resets an explicit list -->
      <button class="chip {((settings[key] as string[]) ?? []).length === 0 ? 'on' : ''}" onclick={() => onpatch({ [key]: [] })}>all</button>
      {#each choices[h.choices ?? ''] ?? [] as c}
        <button class="chip {((settings[key] as string[]) ?? []).includes(c) ? 'on' : ''}" onclick={() => toggle(key, c)}>{c}</button>
      {/each}
    {:else if h.type === 'number'}
      <div class="row">
        <input type="range" min={h.min ?? 0} max={h.max ?? 100} step={h.step ?? 1} value={Number(settings[key])}
          onchange={(e) => onpatch({ [key]: Number(val(e).value) })} />
        <span>{settings[key]}</span>
      </div>
    {:else if h.type === 'bool'}
      <input type="checkbox" checked={Boolean(settings[key])} onchange={(e) => onpatch({ [key]: val(e).checked })} />
    {:else}
      <input type="text" value={String(settings[key] ?? '')} onchange={(e) => onpatch({ [key]: val(e).value })} />
    {/if}
  </div>
{/each}

<style>
  .field { margin: 8px 0; }
</style>
