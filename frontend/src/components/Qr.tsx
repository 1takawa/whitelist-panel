import QRCode from 'qrcode'
import { useEffect, useState } from 'react'

// Always dark-on-white so the code scans in dark mode too. The SVG is generated locally from the link.
export default function Qr({ text, size = 220 }: { text: string; size?: number }) {
  const [svg, setSvg] = useState('')
  useEffect(() => {
    let live = true
    QRCode.toString(text, { type: 'svg', margin: 2, errorCorrectionLevel: 'M', color: { dark: '#000000', light: '#ffffff' } })
      .then((s) => live && setSvg(s))
      .catch(() => live && setSvg(''))
    return () => {
      live = false
    }
  }, [text])
  if (!svg) return <p className="text-sm text-dim">Ссылка слишком длинная для QR-кода.</p>
  return <div role="img" aria-label="QR-код" style={{ width: size, height: size }} className="overflow-hidden rounded-lg bg-white [&>svg]:size-full" dangerouslySetInnerHTML={{ __html: svg }} />
}
