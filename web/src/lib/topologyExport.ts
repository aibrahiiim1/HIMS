// Topology export — turn the ALREADY-RENDERED cytoscape graph into a shareable
// PNG or PDF. No fake data: the pixels come straight from the live cy instance
// (cy.png), and the PDF embeds that same raster. Zero heavy dependencies — the
// PDF is a hand-built single-image document (see encodeImagePdf).
import type { Core } from 'cytoscape'
import { LAYER_COLOR, CONF_COLOR } from '../components/topologyColors'

export type TopologyExportMeta = {
  title: string
  subtitle: string // e.g. "Nodes 42 · Links 51 · 3 low-confidence"
  stamp: string // human timestamp
  layers: Record<string, number>
  theme: 'light' | 'dark'
}

function loadImage(uri: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => resolve(img)
    img.onerror = () => reject(new Error('failed to load rendered topology image'))
    img.src = uri
  })
}

// composeTopologyCanvas renders the graph plus a title/timestamp header and a
// layer + link-confidence legend onto one canvas, so the export carries context.
export async function composeTopologyCanvas(cy: Core, meta: TopologyExportMeta): Promise<HTMLCanvasElement> {
  const dark = meta.theme === 'dark'
  const bg = dark ? '#0f1830' : '#ffffff'
  const fg = dark ? '#e6edf7' : '#1a2433'
  const faint = dark ? '#8aa0bf' : '#64748b'

  // Full graph (not just the viewport), hi-res, on a solid theme background so
  // the export is readable in both light and dark mode.
  const graphUri = cy.png({ full: true, scale: 2, bg })
  const img = await loadImage(graphUri)

  const pad = 48
  const headerH = 104
  const legendH = 56
  const contentW = Math.max(img.width, 820)
  const canvas = document.createElement('canvas')
  canvas.width = contentW + pad * 2
  canvas.height = headerH + img.height + legendH + pad
  const ctx = canvas.getContext('2d')!
  const font = "'Segoe UI', Roboto, system-ui, sans-serif"

  ctx.fillStyle = bg
  ctx.fillRect(0, 0, canvas.width, canvas.height)

  // Header: title + subtitle + timestamp.
  ctx.textBaseline = 'top'
  ctx.fillStyle = fg
  ctx.font = `700 30px ${font}`
  ctx.fillText(meta.title, pad, 30)
  ctx.fillStyle = faint
  ctx.font = `15px ${font}`
  ctx.fillText(meta.subtitle, pad, 70)
  ctx.textAlign = 'right'
  ctx.fillText(meta.stamp, canvas.width - pad, 70)
  ctx.textAlign = 'left'

  // Divider under the header.
  ctx.strokeStyle = dark ? '#26324d' : '#e2e8f0'
  ctx.lineWidth = 1
  ctx.beginPath()
  ctx.moveTo(pad, headerH - 12)
  ctx.lineTo(canvas.width - pad, headerH - 12)
  ctx.stroke()

  // Graph, centered horizontally.
  ctx.drawImage(img, Math.round((canvas.width - img.width) / 2), headerH)

  // Legend: layer swatches (only layers actually present) + link confidence.
  const ly = headerH + img.height + 14
  let lx = pad
  ctx.font = `14px ${font}`
  ctx.textBaseline = 'middle'
  const midY = ly + 9
  for (const [k, c] of Object.entries(LAYER_COLOR)) {
    if (!(meta.layers[k] > 0)) continue
    ctx.fillStyle = c
    ctx.beginPath()
    ctx.arc(lx + 7, midY, 7, 0, Math.PI * 2)
    ctx.fill()
    ctx.fillStyle = fg
    ctx.fillText(k, lx + 20, midY)
    lx += 30 + ctx.measureText(k).width
  }
  for (const [k, c] of Object.entries(CONF_COLOR)) {
    ctx.strokeStyle = c
    ctx.lineWidth = 3
    ctx.beginPath()
    ctx.moveTo(lx, midY)
    ctx.lineTo(lx + 26, midY)
    ctx.stroke()
    const lbl = `${k} link`
    ctx.fillStyle = fg
    ctx.fillText(lbl, lx + 32, midY)
    lx += 32 + 26 + ctx.measureText(lbl).width
  }

  return canvas
}

function canvasToBlob(canvas: HTMLCanvasElement, type: string, quality?: number): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('canvas export failed'))), type, quality)
  })
}

export async function topologyPngBlob(cy: Core, meta: TopologyExportMeta): Promise<Blob> {
  const canvas = await composeTopologyCanvas(cy, meta)
  return canvasToBlob(canvas, 'image/png')
}

// encodeImagePdf builds a minimal, valid single-page PDF that embeds one JPEG
// (DCTDecode) at full page size. This is a tiny, well-defined subset of the PDF
// spec — far lighter than pulling in a PDF library for one raster page. Byte
// offsets for the xref table are computed exactly so the file opens everywhere.
function encodeImagePdf(jpeg: Uint8Array, w: number, h: number): Blob {
  const enc = new TextEncoder()
  const objects: (string | Uint8Array)[] = []
  // 1 catalog, 2 pages, 3 page, 4 image xobject, 5 content
  objects.push(`<< /Type /Catalog /Pages 2 0 R >>`)
  objects.push(`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`)
  objects.push(
    `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 ${w} ${h}] ` +
      `/Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>`,
  )
  objects.push(
    new Uint8Array([
      ...enc.encode(
        `<< /Type /XObject /Subtype /Image /Width ${w} /Height ${h} ` +
          `/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length ${jpeg.length} >>\nstream\n`,
      ),
      ...jpeg,
      ...enc.encode(`\nendstream`),
    ]),
  )
  const content = `q ${w} 0 0 ${h} 0 0 cm /Im0 Do Q`
  objects.push(`<< /Length ${content.length} >>\nstream\n${content}\nendstream`)

  // Assemble with exact byte offsets.
  const parts: Uint8Array[] = []
  let offset = 0
  const push = (u: Uint8Array) => {
    parts.push(u)
    offset += u.length
  }
  const offsets: number[] = []
  push(enc.encode(`%PDF-1.4\n%\xFF\xFF\xFF\xFF\n`))
  objects.forEach((body, i) => {
    offsets[i] = offset
    push(enc.encode(`${i + 1} 0 obj\n`))
    push(typeof body === 'string' ? enc.encode(body) : body)
    push(enc.encode(`\nendobj\n`))
  })
  const xrefStart = offset
  let xref = `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`
  for (let i = 0; i < objects.length; i++) {
    xref += `${String(offsets[i]).padStart(10, '0')} 00000 n \n`
  }
  push(enc.encode(xref))
  push(
    enc.encode(
      `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xrefStart}\n%%EOF`,
    ),
  )
  return new Blob(parts as BlobPart[], { type: 'application/pdf' })
}

function dataUrlToBytes(dataUrl: string): Uint8Array {
  const b64 = dataUrl.slice(dataUrl.indexOf(',') + 1)
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

export async function topologyPdfBlob(cy: Core, meta: TopologyExportMeta): Promise<Blob> {
  const canvas = await composeTopologyCanvas(cy, meta)
  // JPEG keeps the PDF small; 0.92 stays crisp for a network diagram.
  const jpeg = dataUrlToBytes(canvas.toDataURL('image/jpeg', 0.92))
  return encodeImagePdf(jpeg, canvas.width, canvas.height)
}
