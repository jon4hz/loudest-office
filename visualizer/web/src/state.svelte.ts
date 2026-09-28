import * as api from './api'

// store is the one shared document: the last /state answer and the lists
// the tabs need. Polling keeps several phones consistent.
export const store = $state({
  state: null as api.State | null,
  bubbles: [] as api.Bubble[],
  palettes: [] as api.PaletteInfo[],
  presets: [] as string[],
  options: null as api.Options | null,
  error: '',
})

// offline is set while store.error holds a fetch failure of refresh or
// loadAll, so a later good refresh clears only that and never an error run set.
let offline = false

export async function refresh() {
  try {
    store.state = await api.getState()
    if (offline) store.error = ''
    offline = false
  } catch (e) { store.error = String(e); offline = true }
}

export async function loadAll() {
  try {
    ;[store.bubbles, store.palettes, store.presets, store.options] = await Promise.all([api.getBubbles(), api.getPalettes(), api.getPresets(), api.getOptions()])
  } catch (e) { store.error = String(e); offline = true }
}

// run awaits an API call, refreshes what it changed and shows its error. It
// reports whether the call succeeded.
export async function run(fn: () => Promise<unknown>, reload: 'state' | 'all' = 'state'): Promise<boolean> {
  store.error = ''
  offline = false
  let ok = true
  try { await fn() } catch (e) { store.error = String(e); ok = false }
  await (reload === 'all' ? Promise.all([refresh(), loadAll()]) : refresh())
  return ok
}

export function startPolling(): () => void {
  const tick = () => { if (!document.hidden) refresh() }
  tick()
  const id = setInterval(tick, 2000)
  return () => clearInterval(id)
}
