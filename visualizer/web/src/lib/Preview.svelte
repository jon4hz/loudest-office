<script lang="ts">
  import { frames } from '../api'
  let { name }: { name: string } = $props()
  let canvas: HTMLCanvasElement
  let ctx: CanvasRenderingContext2D | undefined
  let img: ImageData | undefined
  $effect(() => {
    const stop = frames(name, (w, h, rgb) => {
      if (w === 0 || h === 0) return // no panel and no terminal yet: nothing to draw
      ctx ??= canvas.getContext('2d')!
      if (!img || img.width !== w || img.height !== h) {
        canvas.width = w; canvas.height = h
        img = ctx.createImageData(w, h)
      }
      for (let i = 0, j = 0; i < rgb.length; i += 3, j += 4) {
        img.data[j] = rgb[i]; img.data[j + 1] = rgb[i + 1]; img.data[j + 2] = rgb[i + 2]; img.data[j + 3] = 255
      }
      ctx.putImageData(img, 0, 0)
    })
    return stop
  })
</script>

<canvas bind:this={canvas} width="64" height="32"></canvas>

<style>
  canvas { width: 100%; image-rendering: pixelated; background: #000; border-radius: 12px; }
</style>
