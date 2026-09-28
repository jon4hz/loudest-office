// Typed client for /api/v1. Every URL is relative to the page, so the UI
// works at / and behind a prefix alike.
export type Hint = { type: 'multi' | 'number' | 'bool' | 'string'; choices?: string; min?: number; max?: number; step?: number }
export type Options = { palettes: string[]; layouts: string[]; peaks: string[]; levels: string[]; orders: string[]; bubbles: Record<string, Record<string, Hint>> }
export type Loop = { loop: number; order: string }
export type Settings = { music: Loop; idle: Loop; show: string; music_db: number; silence_db: number; silence_after: number }
export type Event = { id: string; text: string; level: string; expires: string }
export type PanelStatus = { frames_ok: number; crc_err: number; seq_gaps: number; fps: number; temp_c: number }
export type PanelSpec = { name: string; kind: 'serial' | 'virtual' | 'terminal'; address?: string; baud?: number; w?: number; h?: number; fps: number; brightness?: number; idle_brightness?: number }
export type PanelState = PanelSpec & { connected: boolean; clients: number; status?: PanelStatus }
export type State = { active: string; kind: string; playing: boolean; db: number; bpm: number; settings: Settings; events: Event[]; panels: PanelState[]; preset: string; modified: boolean }
export type Bubble = { name: string; kind: string; weight: number; settings: Record<string, unknown> }
export type Stop = { pos: number; color: string }
export type Custom = { name?: string; stops: Stop[]; axis: 'bands' | 'height'; peak: string }
export type PaletteInfo = { name: string; preview: string[]; custom: Custom | null }

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch('api/v1/' + path, {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  if (!res.ok) {
    let msg = text
    try { msg = JSON.parse(text).error ?? text } catch { /* plain text error */ }
    throw new Error(msg)
  }
  return text ? JSON.parse(text) : (undefined as T)
}

export const getState = () => req<State>('GET', 'state')
// patchController takes any subset of Settings; nested loops merge field by field server-side.
export const patchController = (patch: Record<string, unknown>) => req<State>('PATCH', 'controller', patch)
export const getBubbles = () => req<Bubble[]>('GET', 'bubbles')
export const patchBubble = (name: string, patch: { weight?: number; settings?: Record<string, unknown> }) => req<Bubble>('PATCH', `bubbles/${encodeURIComponent(name)}`, patch)
export const show = (name: string, seconds: number) => req<void>('POST', `bubbles/${encodeURIComponent(name)}/show`, { seconds })
export const getOptions = () => req<Options>('GET', 'options')
export const next = () => req<void>('POST', 'next')
export const postEvent = (text: string, level: string, ttl: number) => req<{ id: string }>('POST', 'events', { text, level, ttl })
export const deleteEvent = (id: string) => req<void>('DELETE', `events/${encodeURIComponent(id)}`)
export const getPalettes = () => req<PaletteInfo[]>('GET', 'palettes')
export const putPalette = (name: string, p: Custom) => req<PaletteInfo>('PUT', `palettes/${encodeURIComponent(name)}`, { stops: p.stops, axis: p.axis, peak: p.peak })
export const deletePalette = (name: string) => req<void>('DELETE', `palettes/${encodeURIComponent(name)}`)
export const getPresets = () => req<string[]>('GET', 'presets')
export const savePreset = (name: string) => req<{ name: string }>('PUT', `presets/${encodeURIComponent(name)}`)
export const loadPreset = (name: string) => req<State>('POST', `presets/${encodeURIComponent(name)}/load`)
export const deletePreset = (name: string) => req<void>('DELETE', `presets/${encodeURIComponent(name)}`)

export const getPanels = () => req<PanelState[]>('GET', 'panels')
export const putPanel = (name: string, spec: Omit<PanelSpec, 'name'>) => req<PanelState>('PUT', `panels/${encodeURIComponent(name)}`, spec)
export const patchPanel = (name: string, patch: Partial<PanelSpec>) => req<PanelState>('PATCH', `panels/${encodeURIComponent(name)}`, patch)
export const deletePanel = (name: string) => req<void>('DELETE', `panels/${encodeURIComponent(name)}`)

// frames opens the frame websocket for one panel and calls onFrame per frame
// with the panel size and RGB888 pixels. It reconnects with backoff until the
// returned stop function is called.
export function frames(name: string, onFrame: (w: number, h: number, rgb: Uint8Array) => void): () => void {
  let ws: WebSocket | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  let delay = 1000
  let stopped = false
  const url = new URL(`api/v1/panels/${encodeURIComponent(name)}/frames`, location.href)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  const connect = () => {
    ws = new WebSocket(url)
    ws.binaryType = 'arraybuffer'
    ws.onopen = () => { delay = 1000 }
    ws.onmessage = (e: MessageEvent<ArrayBuffer>) => {
      const v = new DataView(e.data)
      onFrame(v.getUint16(0, true), v.getUint16(2, true), new Uint8Array(e.data, 4))
    }
    ws.onclose = () => {
      if (stopped) return
      timer = setTimeout(connect, delay)
      delay = Math.min(delay * 2, 10000)
    }
  }
  connect()
  return () => { stopped = true; clearTimeout(timer); ws?.close() }
}
