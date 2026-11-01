import type { Doc } from '../../types'

// Dotted-path access to the nested documents of an inbound ("streamSettings.realitySettings.target").

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function getPath(o: any, path: string): any {
  let cur = o
  for (const k of path.split('.')) {
    if (cur === null || typeof cur !== 'object') return undefined
    cur = cur[k]
  }
  return cur
}

// setPath returns a copy of o with the value stored at path; the objects along the way are created. An undefined
// value removes the key, so an emptied field does not leave "" or NaN in the Xray config.
export function setPath<T>(o: T, path: string, v: unknown): T {
  const keys = path.split('.')
  const walk = (cur: unknown, i: number): unknown => {
    const obj: Doc = cur !== null && typeof cur === 'object' && !Array.isArray(cur) ? { ...(cur as Doc) } : {}
    const k = keys[i]
    if (i === keys.length - 1) {
      if (v === undefined) delete obj[k]
      else obj[k] = v
    } else {
      obj[k] = walk(obj[k], i + 1)
    }
    return obj
  }
  return walk(o, 0) as T
}

// Random short ids the way Xray-панелей makes them: one of each even length from 2 to 16, in random order.
export function randomShortIds(): string[] {
  const lens = [2, 4, 6, 8, 10, 12, 14, 16]
  for (let i = lens.length - 1; i > 0; i--) {
    const j = crypto.getRandomValues(new Uint32Array(1))[0] % (i + 1)
    ;[lens[i], lens[j]] = [lens[j], lens[i]]
  }
  return lens.map((n) => {
    const b = crypto.getRandomValues(new Uint8Array(n / 2))
    return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
  })
}

export function randomPath(): string {
  const b = crypto.getRandomValues(new Uint8Array(6))
  return '/' + Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

// VLESS encryption strings carry the handshake mode in their second segment.
export function withEncMode(s: string, mode: string): string {
  const p = s.split('.')
  if (p.length < 4 || !p[0].startsWith('mlkem768x25519plus')) return s
  p[1] = mode
  return p.join('.')
}

export const GiB = 1024 * 1024 * 1024
