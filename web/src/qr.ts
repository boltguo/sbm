import QRCode from 'qrcode'

const cardWidth = 720
const cardPadding = 48
const qrSize = cardWidth - cardPadding * 2
const titleGap = 28
const bottomPadding = 42
const maxTitleWidth = cardWidth - cardPadding * 2
const titleFontFamily = 'ui-monospace, "SFMono-Regular", "Cascadia Mono", "Noto Sans CJK SC", monospace'

function wrapTitle(context: CanvasRenderingContext2D, value: string, maxWidth: number) {
  const lines: string[] = []
  let line = ''

  for (const character of Array.from(value)) {
    const candidate = line + character
    if (!line || context.measureText(candidate).width <= maxWidth) {
      line = candidate
      continue
    }
    lines.push(line.trimEnd())
    line = character.trimStart()
  }
  if (line) lines.push(line.trimEnd())
  return lines
}

function fitTitle(context: CanvasRenderingContext2D, value: string) {
  let fontSize = 36
  let lines: string[] = []

  while (true) {
    context.font = `700 ${fontSize}px ${titleFontFamily}`
    lines = wrapTitle(context, value, maxTitleWidth)
    if (lines.length <= 2 || fontSize === 22) break
    fontSize -= 2
  }

  if (lines.length > 2) {
    lines = lines.slice(0, 2)
    let lastLine = lines[1]
    while (lastLine && context.measureText(`${lastLine}…`).width > maxTitleWidth) {
      lastLine = Array.from(lastLine).slice(0, -1).join('')
    }
    lines[1] = `${lastLine}…`
  }

  return { fontSize, lines }
}

export async function createQrCard(value: string, name: string) {
  const qrCanvas = document.createElement('canvas')
  await QRCode.toCanvas(qrCanvas, value, {
    width: qrSize,
    margin: 2,
    errorCorrectionLevel: 'M',
    color: { dark: '#111712', light: '#f4f1e8' },
  })

  const label = name.trim() || 'SBM'
  const measureCanvas = document.createElement('canvas')
  const measureContext = measureCanvas.getContext('2d')
  if (!measureContext) throw new Error('Canvas is not available')
  const title = fitTitle(measureContext, label)
  const lineHeight = Math.round(title.fontSize * 1.35)

  const canvas = document.createElement('canvas')
  canvas.width = cardWidth
  canvas.height = cardPadding + qrSize + titleGap + lineHeight * title.lines.length + bottomPadding
  const context = canvas.getContext('2d')
  if (!context) throw new Error('Canvas is not available')

  context.fillStyle = '#eaf7c7'
  context.fillRect(0, 0, canvas.width, canvas.height)
  context.imageSmoothingEnabled = false
  context.drawImage(qrCanvas, cardPadding, cardPadding, qrSize, qrSize)

  context.fillStyle = '#111712'
  context.font = `700 ${title.fontSize}px ${titleFontFamily}`
  context.textAlign = 'center'
  context.textBaseline = 'top'
  const titleTop = cardPadding + qrSize + titleGap
  title.lines.forEach((line, index) => context.fillText(line, cardWidth / 2, titleTop + index * lineHeight))

  return canvas.toDataURL('image/png')
}

export function downloadQrCard(dataURL: string, name: string) {
  const safeName = name
    .trim()
    .replace(/[<>:"/\\|?*\u0000-\u001f]/g, '-')
    .replace(/\s+/g, ' ')
    .slice(0, 80) || 'SBM'
  const anchor = document.createElement('a')
  anchor.href = dataURL
  anchor.download = `${safeName}-QR.png`
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
}
