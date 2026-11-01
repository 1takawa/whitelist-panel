// Happ routing profiles: the editor builds a JSON profile and encodes it into a happ://routing link,
// which is what the panel sends in the Routing header.

export type RoutingProfile = {
  Name: string
  GlobalProxy: 'true' | 'false'
  DomainStrategy: string
  RouteOrder?: string
  DirectSites: string[]
  DirectIp: string[]
  ProxySites: string[]
  ProxyIp: string[]
  BlockSites: string[]
  BlockIp: string[]
  [extra: string]: unknown
}

export type RoutingMode = 'onadd' | 'add'

/** UTF-8 safe base64: btoa alone breaks on non-Latin1 text such as Cyrillic profile names. */
export function toBase64Utf8(text: string): string {
  const bytes = new TextEncoder().encode(text)
  let bin = ''
  bytes.forEach((b) => { bin += String.fromCharCode(b) })
  return btoa(bin)
}

export function buildLink(profile: RoutingProfile, mode: RoutingMode = 'onadd'): string {
  return `happ://routing/${mode}/${toBase64Utf8(JSON.stringify(profile))}`
}

export function emptyProfile(): RoutingProfile {
  return { Name: 'Custom Rules', GlobalProxy: 'true', DomainStrategy: 'IPIfNonMatch', DirectSites: [], DirectIp: [], ProxySites: [], ProxyIp: [], BlockSites: [], BlockIp: [] }
}

export type ParseResult = { ok: true; profile: RoutingProfile; mode: RoutingMode } | { ok: false; reason: 'empty' | 'off' | 'invalid' }

/** Reads whatever is stored: a happ://routing link, a JSON profile, or nothing. */
export function parseStored(value: string): ParseResult {
  const src = value.trim()
  if (!src) return { ok: false, reason: 'empty' }
  if (src === 'happ://routing/off') return { ok: false, reason: 'off' }
  let json = src
  let mode: RoutingMode = 'onadd'
  if (!src.startsWith('{')) {
    const m = /^happ:\/\/routing\/(onadd|add)\/([A-Za-z0-9+/_-]+={0,2})$/.exec(src)
    if (!m) return { ok: false, reason: 'invalid' }
    mode = m[1] as RoutingMode
    try {
      const bin = atob(m[2].replace(/-/g, '+').replace(/_/g, '/'))
      json = new TextDecoder('utf-8', { fatal: true }).decode(Uint8Array.from(bin, (c) => c.charCodeAt(0)))
    } catch {
      return { ok: false, reason: 'invalid' }
    }
  }
  try {
    const p = JSON.parse(json) as RoutingProfile
    if (typeof p !== 'object' || p === null || Array.isArray(p)) return { ok: false, reason: 'invalid' }
    const list = (v: unknown) => (Array.isArray(v) ? v.map(String) : [])
    return {
      ok: true,
      mode,
      profile: { ...p, Name: String(p.Name ?? ''), GlobalProxy: p.GlobalProxy === 'false' ? 'false' : 'true', DomainStrategy: String(p.DomainStrategy ?? 'IPIfNonMatch'),
        DirectSites: list(p.DirectSites), DirectIp: list(p.DirectIp), ProxySites: list(p.ProxySites), ProxyIp: list(p.ProxyIp), BlockSites: list(p.BlockSites), BlockIp: list(p.BlockIp) },
    }
  } catch {
    return { ok: false, reason: 'invalid' }
  }
}

/** One rule per line: commas can belong to regular-expression rules. */
export function toLines(text: string): string[] {
  return text.split(/\r?\n/).map((l) => l.trim()).filter(Boolean)
}


export type RoutingLinkInfo = { name: string; updated: Date | null; geoip: string; geosite: string }

/** Reads the name, date and geo files out of a happ:// or incy:// routing link; null when it is not one. */
export function decodeRoutingLink(link: string): RoutingLinkInfo | null {
  const m = /^(?:happ|incy):\/\/routing\/(?:onadd|add)\/([A-Za-z0-9+/_-]+={0,2})$/.exec(link.trim())
  if (!m) return null
  try {
    const bin = atob(m[1].replace(/-/g, '+').replace(/_/g, '/'))
    const p = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(Uint8Array.from(bin, (c) => c.charCodeAt(0)))) as Record<string, unknown>
    const secs = Number(p.LastUpdated)
    return { name: String(p.Name ?? ''), updated: secs > 0 ? new Date(secs * 1000) : null, geoip: String(p.Geoipurl ?? ''), geosite: String(p.Geositeurl ?? '') }
  } catch {
    return null
  }
}

/** The dated release tag jsDelivr URLs carry ("…/roscomvpn-geoip@202610310932/…"), "" when the URL is not pinned. */
export function geoVersion(url: string): string {
  return /@(\d{6,})\//.exec(url)?.[1] ?? ''
}
