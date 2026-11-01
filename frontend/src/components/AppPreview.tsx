import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { api } from '../api'
import { formatBytes } from '../lib'
import type { HappBanner, SubSettings } from '../types'

export type AppId = 'happ' | 'incy'
type Theme = 'dark' | 'light'
type State = 'active' | 'trial' | 'expired' | 'depleted'
type HappView = 'phone' | 'desktop'

type PreviewServer = { name: string; protocol: string; network: string; security: string; country: string }

type Preview = {
  state: State
  banner: HappBanner
  email: string
  title: string
  announce: string
  supportUrl: string
  profileUrl: string
  servers: PreviewServer[]
  infoName: string
  onlyInfo: boolean
  used: number
  total: number
  expireDate: string
  daysLeft: number
}

// Happ, drawn from its own screenshots (Android phone and Windows 4.3.0): white cards on a soft grey background,
// indigo icons, navy server names.
const happPalettes = {
  light: {
    bg: 'linear-gradient(160deg, #f1f1f3 0%, #e8e8eb 55%, #ececef 100%)', win: '#f1f1f3', card: '#ffffff', row: '#fafafa', rowSel: '#eef0ff', selBar: '#4f6bff',
    name: '#1f1d3a', sub: '#9d9dab', icon: '#7c79b8', text: '#15151c', track: '#f3f3f7', trackLine: '#cdcdd8', fill: '#d9dcfb', line: '#ececf1', btn: '#ffffff',
  },
  dark: {
    bg: 'linear-gradient(160deg, #17181c 0%, #121316 100%)', win: '#17181c', card: '#212228', row: '#26272d', rowSel: '#2a2f4d', selBar: '#6f87ff',
    name: '#f2f2f7', sub: '#8d8d9b', icon: '#a09ddb', text: '#f4f4f8', track: '#2b2c33', trackLine: '#3d3e47', fill: '#3a3e63', line: '#2d2e35', btn: '#2b2c33',
  },
}
type HappPalette = (typeof happPalettes)['light']

const OK = '#22b45c'
const BAD = '#e5484d'
const BLUE = '#3478ff'

const ping = [38, 52, 71]

function Icon({ d, size = 18, color = 'currentColor', fill = false, width = 2 }: { d: string; size?: number; color?: string; fill?: boolean; width?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill={fill ? color : 'none'} stroke={color} strokeWidth={width} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d={d} />
    </svg>
  )
}

const icons = {
  gear: 'M12 15a3 3 0 100-6 3 3 0 000 6zM19.4 15a1.7 1.7 0 00.3 1.8l.1.1a2 2 0 11-2.8 2.8l-.1-.1a1.7 1.7 0 00-1.8-.3 1.7 1.7 0 00-1 1.5V21a2 2 0 11-4 0v-.1a1.7 1.7 0 00-1.1-1.5 1.7 1.7 0 00-1.8.3l-.1.1a2 2 0 11-2.8-2.8l.1-.1a1.7 1.7 0 00.3-1.8 1.7 1.7 0 00-1.5-1H3a2 2 0 110-4h.1a1.7 1.7 0 001.5-1.1 1.7 1.7 0 00-.3-1.8l-.1-.1a2 2 0 112.8-2.8l.1.1a1.7 1.7 0 001.8.3H9a1.7 1.7 0 001-1.5V3a2 2 0 114 0v.1a1.7 1.7 0 001 1.5 1.7 1.7 0 001.8-.3l.1-.1a2 2 0 112.8 2.8l-.1.1a1.7 1.7 0 00-.3 1.8V9a1.7 1.7 0 001.5 1H21a2 2 0 110 4h-.1a1.7 1.7 0 00-1.5 1z',
  plus: 'M12 5v14M5 12h14',
  power: 'M18.4 6.6a9 9 0 11-12.8 0M12 2v10',
  chevron: 'M6 9l6 6 6-6',
  chevronUp: 'M6 15l6-6 6 6',
  chevronRight: 'M9 6l6 6-6 6',
  info: 'M12 16v-4M12 8h.01M22 12a10 10 0 11-20 0 10 10 0 0120 0z',
  chat: 'M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z',
  user: 'M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2M12 11a4 4 0 100-8 4 4 0 000 8z',
  globe: 'M12 22a10 10 0 100-20 10 10 0 000 20zM2 12h20M12 2a15 15 0 010 20M12 2a15 15 0 000 20',
  bell: 'M18 8a6 6 0 10-12 0c0 7-3 9-3 9h18s-3-2-3-9M13.7 21a2 2 0 01-3.4 0',
  mail: 'M4 4h16a2 2 0 012 2v12a2 2 0 01-2 2H4a2 2 0 01-2-2V6a2 2 0 012-2zM22 6l-10 7L2 6',
  star: 'M12 2l3.1 6.3 6.9 1-5 4.9 1.2 6.8L12 17.8 5.8 21l1.2-6.8-5-4.9 6.9-1z',
  megaphone: 'M3 11v2a1 1 0 001 1h2l5 4V6L6 10H4a1 1 0 00-1 1zM15 9a4 4 0 010 6',
  refresh: 'M21 12a9 9 0 01-15.5 6.2M3 12A9 9 0 0118.5 5.8M18 2v4h-4M6 22v-4h4',
  gauge: 'M12 14l4-5M3.5 18A10 10 0 1120.5 18',
  dots: 'M5 12h.01M12 12h.01M19 12h.01',
  search: 'M11 19a8 8 0 100-16 8 8 0 000 16zM21 21l-4.3-4.3',
  plane: 'M22 2L11 13M22 2l-7 20-4-9-9-4z',
  shield: 'M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z',
  server: 'M4 4h16v6H4zM4 14h16v6H4zM8 7h.01M8 17h.01',
  help: 'M12 22a10 10 0 100-20 10 10 0 000 20zM9.1 9a3 3 0 015.8 1c0 2-3 3-3 3M12 17h.01',
  chevronLeft: 'M15 18l-6-6 6-6',
  bolt: 'M13 2L3 14h9l-1 8 10-12h-9z',
  share: 'M18 8a3 3 0 100-6 3 3 0 000 6zM6 15a3 3 0 100-6 3 3 0 000 6zM18 22a3 3 0 100-6 3 3 0 000 6zM8.6 13.5l6.8 4M15.4 6.5l-6.8 4',
}

const ellipsis: CSSProperties = { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 }
const shrink: CSSProperties = { gridTemplateColumns: 'minmax(0, 1fr)' }

function validColor(v: string, fallback: string): string {
  return /^#?[0-9a-fA-F]{6}$/.test(v.trim()) ? '#' + v.trim().replace('#', '') : fallback
}

function dateRu(iso: string): string {
  const [y, m, d] = iso.split('-')
  return y && m && d ? `${d}.${m}.${y}` : iso
}

const two = (n: number) => String(n).padStart(2, '0')
// "31.10.2026 11:30": Happ shows when the subscription was last refreshed.
function stamp(): string {
  const d = new Date()
  return `${two(d.getDate())}.${two(d.getMonth() + 1)}.${d.getFullYear()} ${two(d.getHours())}:${two(d.getMinutes())}`
}

// Happ writes sizes compactly: "26MB / ∞", "9GB / 106GB".
function compact(n: number): string {
  const gb = n / 1073741824
  if (gb >= 1) return (gb < 10 ? gb.toFixed(1).replace(/\.0$/, '') : String(Math.round(gb))) + 'GB'
  const mb = n / 1048576
  if (mb >= 1) return Math.round(mb) + 'MB'
  return Math.round(n / 1024) + 'KB'
}

// ---- flags (small, drawn in code) ----

function Flag({ code, w, h, round }: { code: string; w: number; h: number; round?: boolean }) {
  const style: CSSProperties = { display: 'block', borderRadius: round ? h / 2 : 5, overflow: 'hidden', flex: 'none', boxShadow: '0 0 0 1px rgba(0,0,0,.08)' }
  let body: ReactNode
  switch (code) {
    case 'DE':
      body = <><rect width="30" height="7" y="0" fill="#000" /><rect width="30" height="7" y="7" fill="#dd0000" /><rect width="30" height="6" y="14" fill="#ffce00" /></>
      break
    case 'NL':
      body = <><rect width="30" height="7" y="0" fill="#ae1c28" /><rect width="30" height="7" y="7" fill="#fff" /><rect width="30" height="6" y="14" fill="#21468b" /></>
      break
    case 'FI':
      body = <><rect width="30" height="20" fill="#fff" /><rect x="8" width="4.5" height="20" fill="#003580" /><rect y="8" width="30" height="4.5" fill="#003580" /></>
      break
    default:
      body = <rect width="30" height="20" fill="#c9c9d4" />
  }
  return (
    <svg width={w} height={h} viewBox="0 0 30 20" preserveAspectRatio={round ? 'xMidYMid slice' : 'none'} style={style} aria-hidden="true">
      {body}
    </svg>
  )
}

function Pill({ children, color }: { children: ReactNode; color: string }) {
  return (
    <span style={{ display: 'inline-block', background: color, color: '#fff', borderRadius: 999, padding: '5px 12px', fontSize: 11, fontWeight: 600, whiteSpace: 'nowrap', maxWidth: '100%' }}>
      {children}
    </span>
  )
}

function StatusBar({ color, android }: { color: string; android?: boolean }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: android ? '12px 20px 4px' : '10px 18px 4px', fontSize: 11, fontWeight: 600, color }}>
      <span style={{ fontSize: android ? 13 : 11 }}>{android ? '11:09' : '9:41'}</span>
      <span style={{ display: 'flex', gap: 5, alignItems: 'center' }} aria-hidden="true">
        <span style={{ width: 16, height: 9, borderRadius: 3, border: `1.5px solid ${color}`, position: 'relative', fontSize: 7, lineHeight: '8px', textAlign: 'center' }}>{android ? '71' : ''}</span>
      </span>
    </div>
  )
}

// ---- Happ: shared pieces ----

const happBanner = { blue: BLUE, green: OK, red: BAD } as const

// The traffic bar of a subscription: (i) opens the profile page, the paper plane is the support link, the text is
// "used / total" inside a pill that fills as the quota is used.
function HappBar({ p, h, small }: { p: Preview; h: HappPalette; small?: boolean }) {
  const unlimited = p.total <= 0
  const pct = unlimited ? 0 : Math.min(100, (p.used / p.total) * 100)
  const size = small ? 10 : 11
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '0 12px 8px' }}>
      <span style={{ flex: 'none', width: 14, color: h.icon, visibility: p.profileUrl ? 'visible' : 'hidden' }}><Icon d={icons.info} size={14} width={1.8} /></span>
      <div style={{ flex: 1, position: 'relative', height: 16, borderRadius: 8, background: h.track, border: `1px solid ${h.trackLine}`, overflow: 'hidden' }}>
        <div style={{ position: 'absolute', inset: 0, width: `${pct}%`, background: pct >= 100 ? `${BAD}55` : h.fill }} />
        <div style={{ position: 'relative', textAlign: 'center', fontSize: size - 1, lineHeight: '14px', fontWeight: 600, color: h.text }}>
          {compact(p.used)} / {unlimited ? '∞' : compact(p.total)}
        </div>
      </div>
      <span style={{ flex: 'none', width: 14, color: BLUE, visibility: p.supportUrl ? 'visible' : 'hidden' }}><Icon d={icons.plane} size={14} width={1.8} /></span>
    </div>
  )
}

function HappNotes({ p, s, h }: { p: Preview; s: SubSettings; h: HappPalette }) {
  const ha = s.happ
  const banner = p.banner
  const color = happBanner[(banner.color as keyof typeof happBanner) || 'blue'] ?? BLUE
  return (
    <>
      {p.announce && <div style={{ textAlign: 'center', fontSize: 10.5, color: h.text, padding: '0 12px 8px', lineHeight: 1.35, wordBreak: 'break-word' }}>{p.announce}</div>}
      {banner.text && (
        <div style={{ margin: '0 10px 8px', borderRadius: 12, padding: '8px 10px', background: `${color}22`, border: `1px solid ${color}66`, color: h.text, fontSize: 11, lineHeight: 1.35, display: 'grid', gap: 7, ...shrink }}>
          <span style={{ wordBreak: 'break-word', whiteSpace: 'pre-line' }}>{banner.text}</span>
          {banner.buttonText && <span><Pill color={color}>{banner.buttonText}</Pill></span>}
        </div>
      )}
      {ha.subExpire && p.state === 'expired' && (
        <div style={{ margin: '0 10px 8px', borderRadius: 12, padding: '8px 10px', background: `${BAD}22`, border: `1px solid ${BAD}66`, color: h.text, fontSize: 11, display: 'grid', gap: 7, ...shrink }}>
          <span>Subscription expired{p.expireDate ? ` ${dateRu(p.expireDate)}` : ''}</span>
          <span><Pill color={BAD}>Renew</Pill></span>
        </div>
      )}
    </>
  )
}

function protoLine(sv: PreviewServer, full: boolean): string {
  if (sv.protocol === 'auto') return 'Автоузел: сервер с наименьшей задержкой'
  const parts = [sv.protocol.toUpperCase()]
  if (full) parts.push(sv.network.toUpperCase(), sv.security.toUpperCase())
  return parts.join(' / ')
}

// ---- Happ on a phone (Android) ----

function HappPhone({ p, s, h, dark }: { p: Preview; s: SubSettings; h: HappPalette; dark: boolean }) {
  const when = useMemo(stamp, [])
  const connectable = p.servers.length > 0
  const ring = dark ? 'rgba(130,160,255,.35)' : '#cfe0ea'
  const ringB = dark ? 'rgba(255,140,170,.3)' : '#f0cdd8'
  return (
    <>
      <StatusBar color={h.text} android />
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '10px 18px 0', color: h.text }}>
        <Icon d={icons.gear} size={24} fill color={h.text} width={0.5} />
        <Icon d={icons.plus} size={26} width={2.4} />
      </div>

      <div style={{ display: 'grid', placeItems: 'center', padding: '14px 0 6px' }}>
        <div style={{ width: 150, height: 150, borderRadius: '50%', border: '1.5px solid', borderColor: `${ring} ${ringB} ${ringB} ${ring}`, display: 'grid', placeItems: 'center' }}>
          <div style={{ width: 114, height: 114, borderRadius: '50%', background: dark ? '#23242b' : '#ececee', display: 'grid', placeItems: 'center', boxShadow: dark ? 'inset 0 2px 8px rgba(0,0,0,.5)' : 'inset 0 -3px 8px rgba(0,0,0,.08), 0 6px 18px rgba(0,0,0,.08)' }}>
            <div style={{ width: 94, height: 94, borderRadius: '50%', background: dark ? '#2c2d35' : '#ffffff', display: 'grid', placeItems: 'center', boxShadow: dark ? '0 3px 10px rgba(0,0,0,.5)' : '0 3px 10px rgba(0,0,0,.1)' }}>
              <span style={{ color: connectable ? '#b8b8c6' : '#d6d6dd' }}><Icon d={icons.power} size={32} width={2.2} /></span>
            </div>
          </div>
        </div>
      </div>
      <div style={{ textAlign: 'right', padding: '2px 16px 7px', fontSize: 11, color: h.sub }}>Hide everything</div>

      <div style={{ margin: '0 9px', borderRadius: 16, background: h.card, overflow: 'hidden', boxShadow: '0 1px 2px rgba(0,0,0,.04)' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '10px 12px 8px' }}>
          <span style={{ color: h.icon, flex: 'none' }}><Icon d={icons.chevronUp} size={18} /></span>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ ...ellipsis, fontSize: 16, fontWeight: 700, color: h.name }}>{p.title || 'subscription'}</div>
            <div style={{ fontSize: 8.5, color: h.sub }}>{when}</div>
          </div>
          <span style={{ display: 'flex', gap: 12, color: h.icon, flex: 'none' }}>
            <Icon d={icons.refresh} size={17} /><Icon d={icons.gauge} size={17} /><Icon d={icons.dots} size={17} width={3} />
          </span>
        </div>
        <HappBar p={p} h={h} />
        <HappNotes p={p} s={s} h={h} />
        <div style={{ display: 'grid', ...shrink }}>
          {p.infoName && <HappPhoneRow name={p.infoName} sub="" h={h} flag={<InfoBadge h={h} />} />}
          {p.servers.map((sv, i) => <HappPhoneRow key={i} name={sv.name} sub={protoLine(sv, false)} h={h} selected={i === 0} flag={<Flag code={sv.country} w={34} h={24} />} />)}
        </div>
      </div>
    </>
  )
}

function InfoBadge({ h }: { h: HappPalette }) {
  return <span style={{ width: 34, height: 24, borderRadius: 5, background: h.track, color: h.icon, display: 'grid', placeItems: 'center', flex: 'none' }}><Icon d={icons.info} size={15} /></span>
}

function HappPhoneRow({ name, sub, h, selected, flag }: { name: string; sub: string; h: HappPalette; selected?: boolean; flag: ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 11, padding: '8px 12px 8px 14px', background: selected ? h.rowSel : h.row, borderLeft: `3px solid ${selected ? h.selBar : 'transparent'}`, borderTop: `1px solid ${h.line}` }}>
      {flag}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ ...ellipsis, fontSize: 15, fontWeight: 700, color: h.name }}>{name}</div>
        {sub && <div style={{ fontSize: 10.5, color: h.sub, marginTop: 1 }}>{sub}</div>}
      </div>
      <span style={{ color: h.sub, flex: 'none' }}><Icon d={icons.chevronRight} size={16} width={1.6} /></span>
    </div>
  )
}

// ---- Happ on a computer (Windows 4.3.0, the "Servers" page) ----

function HappDesktop({ p, s, h }: { p: Preview; s: SubSettings; h: HappPalette }) {
  const when = useMemo(stamp, [])
  const hours = s.updateHours || 12
  return (
    <div style={{ background: h.win, minHeight: '100%' }}>
      <div style={{ padding: '14px 12px 12px' }}>
        <div style={{ fontSize: 20, fontWeight: 700, color: h.name, marginBottom: 8 }}>Servers</div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 10 }}>
          <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'space-between', background: h.card, border: `1px solid ${h.line}`, borderRadius: 6, padding: '6px 9px', fontSize: 10.5, color: h.sub }}>
            <span>Type here to search</span><Icon d={icons.search} size={14} />
          </div>
          <span style={{ display: 'flex', gap: 8, color: h.icon }}><Icon d={icons.gauge} size={15} /><Icon d={icons.dots} size={15} width={3} /></span>
        </div>

        <div style={{ background: h.card, borderRadius: 8, overflow: 'hidden' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '9px 10px 6px' }}>
            <span style={{ color: h.sub, flex: 'none' }}><Icon d={icons.chevron} size={15} /></span>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ ...ellipsis, fontSize: 12.5, fontWeight: 700, color: h.name }}>{p.title || 'subscription'}</div>
              <div style={{ ...ellipsis, fontSize: 8.5, color: h.sub }}>{when} | Autoupdate - {hours}h.</div>
            </div>
            <span style={{ display: 'flex', gap: 8, color: h.icon, flex: 'none' }}><Icon d={icons.refresh} size={14} /><Icon d={icons.gauge} size={14} /><Icon d={icons.dots} size={14} width={3} /></span>
          </div>
          <HappBar p={p} h={h} small />
          <HappNotes p={p} s={s} h={h} />
          <div style={{ display: 'grid', ...shrink }}>
            {p.infoName && <HappDesktopRow name={p.infoName} sub="" h={h} flag={<span style={{ width: 18, height: 18, borderRadius: 9, background: h.track, color: h.icon, display: 'grid', placeItems: 'center', flex: 'none' }}><Icon d={icons.info} size={11} /></span>} />}
            {p.servers.map((sv, i) => <HappDesktopRow key={i} name={sv.name} sub={protoLine(sv, true)} h={h} flag={<Flag code={sv.country} w={18} h={18} round />} />)}
          </div>
        </div>
      </div>
    </div>
  )
}

function HappDesktopRow({ name, sub, h, flag }: { name: string; sub: string; h: HappPalette; flag: ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 9, padding: '6px 10px', borderTop: `1px solid ${h.line}` }}>
      {flag}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ ...ellipsis, fontSize: 11.5, color: h.name }}>{name}</div>
        {sub && <div style={{ fontSize: 8, color: h.sub, marginTop: 1 }}>{sub}</div>}
      </div>
      <span style={{ color: h.sub, flex: 'none' }}><Icon d={icons.chevronRight} size={13} width={1.6} /></span>
    </div>
  )
}

// ---- INCY on a phone (iOS, 2.7.0, the "Подключение" screen) ----

// Colours measured from the app's own screenshot (dark, green-tinted); the light set is a guess in the same style.
const incyPhone = {
  dark: { top: '#17271b', bottom: '#0a0f0b', card: 'rgba(255,255,255,.035)', cardLine: 'rgba(195,224,75,.14)', btn: 'rgba(255,255,255,.07)', text: '#e9efe7', dim: '#8d998b', faint: '#5d695c', lime: '#c3e04b', limeBg: 'rgba(195,224,75,.17)', green: '#3ddc84', greenBg: 'rgba(61,220,132,.15)', ring: 'rgba(195,224,75,.32)', core: '#1d2a20', power: '#8a948a', bar: 'rgba(15,26,18,.92)' },
  light: { top: '#eef4e4', bottom: '#f8faf4', card: 'rgba(255,255,255,.75)', cardLine: 'rgba(110,140,20,.22)', btn: 'rgba(0,0,0,.06)', text: '#18200f', dim: '#66705f', faint: '#98a190', lime: '#6f8f0c', limeBg: 'rgba(120,150,20,.16)', green: '#16a05a', greenBg: 'rgba(22,160,90,.13)', ring: 'rgba(110,140,20,.35)', core: '#e3ebd6', power: '#7b8672', bar: 'rgba(255,255,255,.92)' },
}
type IncyPhonePalette = (typeof incyPhone)['dark']

const INCY_PHONE_WIDTH = 390
const INCY_PHONE_HEIGHT = 845

function IncyScreen({ p, s, c }: { p: Preview; s: SubSettings; c: IncyPhonePalette }) {
  const n = s.incy
  const d = useMemo(() => new Date(), [])
  const when = `${two(d.getDate())}.${two(d.getMonth() + 1)}.${String(d.getFullYear()).slice(2)}, ${two(d.getHours())}:${two(d.getMinutes())}`
  const servers = useMemo(() => {
    const rows = p.servers.map((sv, i) => ({ sv, ms: ping[i % ping.length] }))
    if (n.sortOrder === 'ping') rows.sort((a, b) => a.ms - b.ms)
    if (n.sortOrder === 'name') rows.sort((a, b) => a.sv.name.localeCompare(b.sv.name))
    return rows.map((r) => r.sv)
  }, [p.servers, n.sortOrder])
  const unlimited = p.total <= 0
  const pct = unlimited ? 0 : Math.min(100, (p.used / p.total) * 100)
  const count = servers.length + (p.infoName ? 1 : 0)
  const bg = validColor(n.bannerBgColor, '#3b3f8f')
  const btnColor = validColor(n.bannerButtonColor, '#ffffff')
  const btnText = btnColor.toLowerCase() === '#ffffff' ? '#1a1d2a' : '#fff'
  const mono = 'ui-monospace, "SF Mono", Menlo, monospace'
  const round: CSSProperties = { width: 44, height: 44, borderRadius: 22, background: c.btn, display: 'grid', placeItems: 'center', color: c.dim, flex: 'none' }
  const tab = (icon: string, label: string, on?: boolean) => (
    <div key={label} style={{ flex: on ? 1.25 : 1, height: 62, borderRadius: 31, background: on ? c.limeBg : 'transparent', border: on ? `1px solid ${c.cardLine}` : '1px solid transparent', display: 'grid', placeItems: 'center', alignContent: 'center', gap: 3, color: on ? c.text : c.dim, fontSize: 14 }}>
      <span style={{ color: on ? c.lime : c.dim, display: 'flex' }}><Icon d={icon} size={24} /></span>{label}
    </div>
  )
  return (
    <div style={{ position: 'relative', width: INCY_PHONE_WIDTH, height: INCY_PHONE_HEIGHT, overflow: 'hidden', background: `linear-gradient(180deg, ${c.top}, ${c.bottom} 70%)`, color: c.text, fontFamily: font }}>
      <div style={{ height: '100%', overflowY: 'auto', scrollbarWidth: 'none', paddingBottom: 120 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '17px 30px 0 28px', fontSize: 17, fontWeight: 600 }}>
          <span>{two(d.getHours())}:{two(d.getMinutes())}</span>
          <span aria-hidden="true" style={{ display: 'flex', gap: 6, alignItems: 'center', fontSize: 11 }}>
            <span style={{ width: 24, height: 12, borderRadius: 4, border: `1.5px solid ${c.text}`, display: 'grid', placeItems: 'center', fontSize: 9 }}>26</span>
          </span>
        </div>
        <div style={{ padding: '2px 28px 0', fontSize: 13, color: c.dim }}>◂ App Store</div>

        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '34px 16px 0 16px' }}>
          <span style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
            <b style={{ fontSize: 32, letterSpacing: 0.5, color: c.lime }}>INCY</b><span style={{ fontSize: 15, color: c.dim }}>v2.7.0</span>
          </span>
          <span style={{ ...round, borderRadius: 14, width: 48, height: 48 }}><Icon d={icons.globe} size={24} /></span>
        </div>

        <div style={{ display: 'grid', placeItems: 'center', marginTop: 52 }}>
          <div style={{ width: 192, height: 192, borderRadius: 96, border: `2px solid ${c.ring}`, display: 'grid', placeItems: 'center' }}>
            <div style={{ width: 160, height: 160, borderRadius: 80, background: c.core, display: 'grid', placeItems: 'center', color: c.power }}>
              <Icon d={icons.power} size={78} width={2.4} />
            </div>
          </div>
          <div style={{ marginTop: 34, fontSize: 19, color: c.dim }}>Нажмите для подключения</div>
        </div>

        <div style={{ margin: '34px 16px 0', fontSize: 11, letterSpacing: 1, color: c.faint, textTransform: 'uppercase' }}>Текущий провайдер</div>
        <div style={{ margin: '6px 16px 0', borderRadius: 24, border: `1px solid ${c.cardLine}`, background: c.card, padding: '14px 14px 8px' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <span style={{ color: c.dim, display: 'flex' }}><Icon d={icons.chevron} size={20} /></span>
            <b style={{ ...ellipsis, flex: 1, fontSize: 19, fontWeight: 600 }}>{p.title || 'Профиль'}</b>
            <span style={{ fontFamily: mono, fontSize: 12, color: c.faint }}>{s.updateHours || 12}ч</span>
            <span style={round}><Icon d={icons.refresh} size={19} /></span>
            <span style={round}><Icon d={icons.bolt} size={19} /></span>
            <span style={round}><Icon d={icons.dots} size={19} width={3} /></span>
          </div>

          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginTop: 8, fontFamily: mono, fontSize: 13, color: c.dim }}>
            <span>{p.expireDate && p.state !== 'expired' ? `${p.daysLeft} дн.` : p.expireDate ? 'истекла' : '∞'}</span>
            <span style={{ flex: 1, height: 5, borderRadius: 3, background: c.btn, overflow: 'hidden' }}>
              <span style={{ display: 'block', width: `${pct}%`, height: '100%', background: pct >= 100 ? BAD : c.lime }} />
            </span>
            <span>{formatBytes(p.used)} / {unlimited ? '∞' : formatBytes(p.total)}</span>
          </div>

          {n.profileDescription && <div style={{ marginTop: 12, fontSize: 15, color: c.dim, lineHeight: 1.4, wordBreak: 'break-word' }}>{n.profileDescription}</div>}
          {p.announce && <div style={{ marginTop: 12, fontSize: 17, color: c.dim, lineHeight: 1.35, wordBreak: 'break-word', whiteSpace: 'pre-line' }}>{p.announce}</div>}
          {n.bannerText && (
            <div style={{ marginTop: 12, borderRadius: 16, padding: '12px 14px', background: bg, color: '#fff', fontSize: 15, lineHeight: 1.35, display: 'grid', gap: 10, ...shrink }}>
              <span style={{ wordBreak: 'break-word' }}>{n.bannerText}</span>
              {n.bannerButtonText && <span><span style={{ display: 'inline-block', borderRadius: 999, padding: '7px 16px', fontSize: 14, fontWeight: 700, background: btnColor, color: btnText }}>{n.bannerButtonText}</span></span>}
            </div>
          )}
          {(p.supportUrl || p.profileUrl) && (
            <div style={{ marginTop: 12, display: 'flex', gap: 8 }}>
              {p.supportUrl && <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, borderRadius: 12, padding: '10px 14px', fontSize: 17, background: c.greenBg, color: c.green }}><Icon d={icons.help} size={22} />Поддержка</span>}
              {p.profileUrl && <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, borderRadius: 12, padding: '10px 14px', fontSize: 17, background: c.limeBg, color: c.lime }}><Icon d={icons.user} size={22} />Профиль</span>}
            </div>
          )}

          <div style={{ marginTop: 14, display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: 8, fontFamily: mono, fontSize: 13, color: c.faint }}>
            <span>{when}</span><span>·</span><span>{count}</span><Icon d={icons.server} size={18} />
          </div>

          <div style={{ marginTop: 6, display: 'grid', gap: 4, ...shrink }}>
            {p.infoName && (
              <div style={{ display: 'flex', alignItems: 'center', gap: 14, padding: '10px 10px' }}>
                <span style={{ width: 48, height: 48, borderRadius: 14, background: c.btn, color: c.dim, display: 'grid', placeItems: 'center', flex: 'none' }}><Icon d={icons.info} size={22} /></span>
                <span style={{ ...ellipsis, flex: 1, fontSize: 21 }}>{p.infoName}</span>
              </div>
            )}
            {servers.map((sv, i) => (
              <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 14, padding: '10px 10px', borderRadius: 16, background: i === 0 && !p.infoName ? c.limeBg : 'transparent', border: i === 0 && !p.infoName ? `1.5px solid ${c.lime}` : '1.5px solid transparent' }}>
                <span style={{ width: 48, height: 48, borderRadius: 14, background: c.btn, display: 'grid', placeItems: 'center', flex: 'none' }}><Flag code={sv.country} w={34} h={24} /></span>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ ...ellipsis, fontSize: 21 }}>{sv.name}</div>
                  <div style={{ fontSize: 15, marginTop: 2 }}>
                    <span style={{ color: c.lime }}>{sv.protocol.toUpperCase()}</span>
                    <span style={{ color: c.faint }}> · </span><span style={{ color: c.dim }}>{sv.network.toUpperCase()}</span>
                    {sv.security !== 'none' && <><span style={{ color: c.faint }}> · </span><span style={{ color: c.lime }}>{sv.security.toUpperCase()}</span></>}
                  </div>
                </div>
              </div>
            ))}
            {count === 0 && <div style={{ padding: '14px 10px', color: c.dim, fontSize: 17 }}>Нет доступных серверов</div>}
          </div>
        </div>
      </div>

      <div style={{ position: 'absolute', left: 14, right: 14, bottom: 24, borderRadius: 38, padding: 6, background: c.bar, border: `1px solid ${c.cardLine}`, display: 'flex', alignItems: 'center', gap: 4 }}>
        {tab(icons.share, 'Подключение', true)}
        {tab(icons.server, 'Серверы')}
        {tab(icons.gear, 'Настройки')}
      </div>
    </div>
  )
}

// ---- INCY on a computer (3.9.0, the "Серверы" page) ----

// Colours measured from the app's own screenshot (dark); the light set is a guess in the same style.
const incyDesk = {
  dark: { win: '#0e0e0e', side: '#0c0c0c', card: '#131313', line: '#242424', text: '#f2f2f2', dim: '#8a8a8a', faint: '#5d5d5d', navOn: '#1c1e14', lime: '#c3e04b', limeBg: '#2a3114', green: '#3ddc84', greenBg: '#10281a', field: '#141414' },
  light: { win: '#f6f6f3', side: '#efefea', card: '#ffffff', line: '#e2e2da', text: '#16160f', dim: '#6b6b63', faint: '#9a9a90', navOn: '#e7edcf', lime: '#7a9a10', limeBg: '#e3ecbb', green: '#17a05a', greenBg: '#dff3e8', field: '#ffffff' },
}
type IncyDeskPalette = (typeof incyDesk)['dark']

const INCY_WIDTH = 640

function plural(n: number, one: string, few: string, many: string): string {
  const m = n % 100
  if (m > 10 && m < 20) return many
  switch (n % 10) {
    case 1: return one
    case 2: case 3: case 4: return few
  }
  return many
}

// Renders its children at a fixed width and scales them down to the available one, so a window drawn at real
// proportions fits the narrow preview column.
function Scaled({ width, children }: { width: number; children: ReactNode }) {
  const outer = useRef<HTMLDivElement>(null)
  const inner = useRef<HTMLDivElement>(null)
  const [fit, setFit] = useState({ scale: 0.5, height: 0 })
  useLayoutEffect(() => {
    const measure = () => {
      if (!outer.current || !inner.current) return
      const scale = Math.min(1, outer.current.clientWidth / width)
      setFit({ scale, height: inner.current.offsetHeight * scale })
    }
    measure()
    const ro = new ResizeObserver(measure)
    if (outer.current) ro.observe(outer.current)
    if (inner.current) ro.observe(inner.current)
    return () => ro.disconnect()
  }, [width])
  return (
    <div ref={outer} style={{ width: '100%', height: fit.height || undefined, overflow: 'hidden', position: 'relative' }}>
      <div ref={inner} style={{ width, transform: `scale(${fit.scale})`, transformOrigin: 'top left', position: 'absolute', left: 0, top: 0 }}>
        {children}
      </div>
    </div>
  )
}

function IncyDesktop({ p, s, c }: { p: Preview; s: SubSettings; c: IncyDeskPalette }) {
  const n = s.incy
  const d = useMemo(() => new Date(), [])
  const when = `${two(d.getDate())}.${two(d.getMonth() + 1)}.${String(d.getFullYear()).slice(2)}, ${two(d.getHours())}:${two(d.getMinutes())}`
  const servers = useMemo(() => {
    const rows = p.servers.map((sv, i) => ({ sv, ms: ping[i % ping.length] }))
    if (n.sortOrder === 'ping') rows.sort((a, b) => a.ms - b.ms)
    if (n.sortOrder === 'name') rows.sort((a, b) => a.sv.name.localeCompare(b.sv.name))
    return rows.map((r) => r.sv)
  }, [p.servers, n.sortOrder])
  const unlimited = p.total <= 0
  const pct = unlimited ? 0 : Math.min(100, (p.used / p.total) * 100)
  const count = servers.length + (p.infoName ? 1 : 0)
  const bg = validColor(n.bannerBgColor, '#3b3f8f')
  const btnColor = validColor(n.bannerButtonColor, '#ffffff')
  const btnText = btnColor.toLowerCase() === '#ffffff' ? '#1a1d2a' : '#fff'
  const mono = 'ui-monospace, "Cascadia Mono", Consolas, monospace'
  const round: CSSProperties = { width: 28, height: 28, borderRadius: 14, border: `1px solid ${c.line}`, display: 'grid', placeItems: 'center', color: c.dim, flex: 'none' }
  const square: CSSProperties = { width: 36, height: 36, borderRadius: 10, border: `1px solid ${c.line}`, display: 'grid', placeItems: 'center', color: c.dim, flex: 'none' }
  const nav = (icon: string, label: string, on?: boolean) => (
    <div key={label} style={{ display: 'flex', alignItems: 'center', gap: 12, height: 42, padding: '0 14px', borderRadius: 10, fontSize: 15, background: on ? c.navOn : 'transparent', color: on ? c.text : c.dim }}>
      <span style={{ color: on ? c.lime : c.dim, display: 'flex' }}><Icon d={icon} size={20} /></span>{label}
    </div>
  )
  return (
    <div style={{ background: c.win, color: c.text, fontFamily: font }}>
      <div style={{ height: 40, display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 0 0 15px', fontSize: 14, background: c.side, borderBottom: `1px solid ${c.line}` }}>
        <span style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
          <b style={{ color: c.lime, letterSpacing: 0.5 }}>INCY</b><span style={{ fontSize: 12, color: c.faint }}>v3.9.0</span>
        </span>
        <span aria-hidden="true" style={{ display: 'flex', color: c.dim, fontSize: 15 }}>
          {['—', '☐', '✕'].map((g) => <span key={g} style={{ width: 46, textAlign: 'center' }}>{g}</span>)}
        </span>
      </div>
      <div style={{ display: 'flex', minHeight: 520 }}>
        <div style={{ width: 200, flex: 'none', background: c.side, borderRight: `1px solid ${c.line}`, padding: '20px 11px', display: 'flex', flexDirection: 'column' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '0 6px 22px' }}>
            <span style={{ width: 34, height: 34, borderRadius: 9, background: c.lime, color: '#10130a', fontWeight: 800, fontSize: 15, display: 'grid', placeItems: 'center' }}>IN</span>
            <b style={{ fontSize: 20, flex: 1 }}>INCY</b>
            <span style={{ color: c.faint, display: 'flex' }}><Icon d={icons.chevronLeft} size={16} /></span>
          </div>
          <div style={{ display: 'grid', gap: 6 }}>
            {nav(icons.shield, 'Главная')}
            {nav(icons.server, 'Серверы', true)}
            {nav(icons.gear, 'Настройки')}
          </div>
          <div style={{ marginTop: 'auto', padding: '0 6px', fontSize: 11, color: c.faint }}>v3.9.0</div>
        </div>

        <div style={{ flex: 1, minWidth: 0, padding: '22px 22px 26px' }}>
          <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 12 }}>
            <div>
              <div style={{ fontSize: 24, fontWeight: 700 }}>Сервера</div>
              <div style={{ fontSize: 13, color: c.dim, marginTop: 2 }}>{count} {plural(count, 'сервер', 'сервера', 'серверов')} · 1 подписка</div>
            </div>
            <div style={{ display: 'flex', gap: 8 }}>
              <span style={square}><Icon d={icons.chevronUp} size={16} /></span>
              <span style={square}><Icon d={icons.gauge} size={16} /></span>
              <span style={{ ...square, background: c.limeBg, borderColor: c.limeBg, color: c.lime }}><Icon d={icons.plus} size={18} /></span>
            </div>
          </div>

          <div style={{ marginTop: 16, height: 42, borderRadius: 10, border: `1px solid ${c.line}`, background: c.field, display: 'flex', alignItems: 'center', gap: 10, padding: '0 14px', color: c.faint, fontSize: 15 }}>
            <Icon d={icons.search} size={17} />Поиск серверов...
          </div>

          <div style={{ marginTop: 16, borderRadius: 18, border: `1px solid ${c.line}`, background: c.card, padding: '12px 16px 8px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <span style={{ color: c.dim, display: 'flex' }}><Icon d={icons.chevron} size={14} /></span>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ ...ellipsis, fontSize: 14, fontWeight: 700, color: c.dim }}>{p.title || 'Профиль'}</div>
                <div style={{ fontSize: 9, color: c.faint }}>{s.updateHours || 12} ч</div>
              </div>
              <span style={{ fontFamily: mono, fontSize: 10, color: c.dim, whiteSpace: 'nowrap' }}>{when}</span>
              <span style={{ minWidth: 20, height: 18, borderRadius: 9, background: c.limeBg, color: c.lime, fontFamily: mono, fontSize: 11, display: 'grid', placeItems: 'center', padding: '0 5px' }}>{count}</span>
              <span style={round}><Icon d={icons.refresh} size={13} /></span>
              <span style={round}><Icon d={icons.gauge} size={13} /></span>
              <span style={round}><Icon d={icons.dots} size={14} width={3} /></span>
            </div>

            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 10, fontFamily: mono, fontSize: 10, color: c.dim }}>
              <span>{p.expireDate && p.state !== 'expired' ? `${p.daysLeft} дн.` : p.expireDate ? 'истекла' : '∞'}</span>
              <span style={{ flex: 1, height: 3, borderRadius: 2, background: c.line, overflow: 'hidden' }}>
                <span style={{ display: 'block', width: `${pct}%`, height: '100%', background: pct >= 100 ? BAD : c.lime }} />
              </span>
              <span>{formatBytes(p.used)} / {unlimited ? '∞' : formatBytes(p.total)}</span>
            </div>

            {n.profileDescription && <div style={{ marginTop: 12, fontSize: 13, color: c.dim, lineHeight: 1.4, wordBreak: 'break-word' }}>{n.profileDescription}</div>}
            {p.announce && <div style={{ marginTop: 14, fontSize: 14, lineHeight: 1.4, wordBreak: 'break-word', whiteSpace: 'pre-line' }}>{p.announce}</div>}
            {n.bannerText && (
              <div style={{ marginTop: 14, borderRadius: 12, padding: '10px 14px', background: bg, color: '#fff', fontSize: 13.5, lineHeight: 1.4, display: 'grid', gap: 8, ...shrink }}>
                <span style={{ wordBreak: 'break-word' }}>{n.bannerText}</span>
                {n.bannerButtonText && <span><span style={{ display: 'inline-block', borderRadius: 999, padding: '5px 14px', fontSize: 12, fontWeight: 700, background: btnColor, color: btnText }}>{n.bannerButtonText}</span></span>}
              </div>
            )}
            {(p.supportUrl || p.profileUrl) && (
              <div style={{ marginTop: 14, display: 'flex', gap: 8 }}>
                {p.supportUrl && <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, borderRadius: 8, padding: '6px 10px', fontSize: 12, background: c.greenBg, color: c.green }}><Icon d={icons.help} size={13} />Поддержка</span>}
                {p.profileUrl && <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, borderRadius: 8, padding: '6px 10px', fontSize: 12, background: c.limeBg, color: c.lime }}><Icon d={icons.user} size={13} />Профиль</span>}
              </div>
            )}

            <div style={{ marginTop: 10, display: 'grid', ...shrink }}>
              {p.infoName && (
                <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '8px 4px' }}>
                  <span style={{ width: 32, height: 32, borderRadius: 16, background: c.line, color: c.dim, display: 'grid', placeItems: 'center', flex: 'none' }}><Icon d={icons.info} size={16} /></span>
                  <span style={{ ...ellipsis, flex: 1, fontSize: 16, fontWeight: 600 }}>{p.infoName}</span>
                </div>
              )}
              {servers.map((sv, i) => (
                <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '8px 4px' }}>
                  <Flag code={sv.country} w={32} h={32} round />
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ ...ellipsis, fontSize: 16, fontWeight: 600 }}>{sv.name}</div>
                    <div style={{ fontSize: 11, marginTop: 1 }}>
                      <span style={{ color: c.lime }}>{sv.protocol.toUpperCase()}</span>
                      <span style={{ color: c.faint }}> · </span><span style={{ color: c.dim }}>{sv.network.toUpperCase()}</span>
                      {sv.security !== 'none' && <><span style={{ color: c.faint }}> · </span><span style={{ color: c.lime }}>{sv.security.toUpperCase()}</span></>}
                    </div>
                  </div>
                  <span style={{ color: c.faint, display: 'flex' }}><Icon d={icons.dots} size={16} width={3} /></span>
                </div>
              ))}
              {count === 0 && <div style={{ padding: '12px 4px', color: c.dim, fontSize: 14 }}>Нет доступных серверов</div>}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

// ---- the preview ----

const seg = (on: boolean) =>
  `rounded-md px-2.5 py-1 text-xs font-medium ${on ? 'bg-accent text-accent-ink' : 'text-dim hover:bg-surface-2'}`

const font = 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif'

// AppPreview shows how the subscription settings being edited look in the Happ and INCY apps. The text comes from
// the server (the engine that builds real subscriptions) for the unsaved form, so it matches what apps receive.
// With locked set the app is fixed (the Happ and Incy tabs preview only their own app) and the switch is not shown.
export default function AppPreview({ sub, app, onApp, locked = false }: { sub: SubSettings; app: AppId; onApp: (a: AppId) => void; locked?: boolean }) {
  const [theme, setTheme] = useState<Theme>('light')
  const [state, setState] = useState<State>('active')
  const [view, setView] = useState<HappView>('phone')
  const [data, setData] = useState<Preview | null>(null)
  const [error, setError] = useState('')
  const key = useMemo(() => JSON.stringify({ sub, state }), [sub, state])

  useEffect(() => {
    let live = true
    const t = setTimeout(() => {
      api<Preview>('/api/settings/preview-app', { method: 'POST', body: { subscription: sub, state } })
        .then((r) => { if (live) { setData(r); setError('') } })
        .catch((e) => { if (live) setError(e instanceof Error ? e.message : 'не удалось построить') })
    }, 250)
    return () => {
      live = false
      clearTimeout(t)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  const happ = happPalettes[theme]
  const desktop = view === 'desktop'
  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-2">
        {!locked && (
          <div className="flex rounded-lg border border-line p-0.5" role="group" aria-label="Приложение">
            <button type="button" className={seg(app === 'happ')} aria-pressed={app === 'happ'} onClick={() => onApp('happ')}>Happ</button>
            <button type="button" className={seg(app === 'incy')} aria-pressed={app === 'incy'} onClick={() => onApp('incy')}>INCY</button>
          </div>
        )}
        <div className="flex rounded-lg border border-line p-0.5" role="group" aria-label="Тема">
          <button type="button" className={seg(theme === 'light')} aria-pressed={theme === 'light'} onClick={() => setTheme('light')}>Светлая</button>
          <button type="button" className={seg(theme === 'dark')} aria-pressed={theme === 'dark'} onClick={() => setTheme('dark')}>Тёмная</button>
        </div>
        <div className="flex rounded-lg border border-line p-0.5" role="group" aria-label="Устройство">
          <button type="button" className={seg(view === 'phone')} aria-pressed={view === 'phone'} onClick={() => setView('phone')}>Телефон</button>
          <button type="button" className={seg(view === 'desktop')} aria-pressed={view === 'desktop'} onClick={() => setView('desktop')}>Компьютер</button>
        </div>
      </div>
      <div className="flex rounded-lg border border-line p-0.5" role="group" aria-label="Состояние подписки">
        {([['active', 'Активна'], ['trial', 'Тест'], ['expired', 'Срок истёк'], ['depleted', 'Трафик исчерпан']] as const).map(([id, label]) => (
          <button key={id} type="button" className={`flex-1 ${seg(state === id)}`} aria-pressed={state === id} onClick={() => setState(id)}>{label}</button>
        ))}
      </div>

      {desktop && app === 'incy' ? (
        <div role="img" aria-label="Макет INCY для компьютера" className="mx-auto w-full overflow-hidden rounded-lg border border-line shadow-lg" style={{ opacity: data ? 1 : 0.5 }}>
          <Scaled width={INCY_WIDTH}>{data && <IncyDesktop p={data} s={sub} c={incyDesk[theme]} />}</Scaled>
        </div>
      ) : desktop ? (
        <div
          role="img"
          aria-label="Макет Happ для компьютера"
          className="mx-auto w-full overflow-hidden rounded-lg border border-line shadow-lg"
          style={{ fontFamily: font, opacity: data ? 1 : 0.5 }}
        >
          <div style={{ background: theme === 'dark' ? '#0f1013' : '#ffffff', color: happ.text, fontSize: 10.5, padding: '5px 8px', borderBottom: `1px solid ${happ.line}`, display: 'flex', gap: 6, alignItems: 'center' }}>
            <span style={{ width: 11, height: 11, borderRadius: 3, background: '#1b1d24', color: '#fff', fontSize: 7, fontWeight: 800, display: 'grid', placeItems: 'center' }}>H</span>
            Happ 4.3.0 (610)
          </div>
          <div style={{ maxHeight: 560, overflowY: 'auto', scrollbarWidth: 'thin' }}>
            {data && <HappDesktop p={data} s={sub} h={happ} />}
          </div>
        </div>
      ) : (
        <div
          role="img"
          aria-label={`Макет приложения ${app === 'happ' ? 'Happ' : 'INCY'}`}
          className="mx-auto w-full max-w-[300px] rounded-[38px] border-[7px] border-[#1b1d24] bg-[#1b1d24] shadow-lg"
        >
          <div
            style={{
              background: app === 'happ' ? happ.bg : incyPhone[theme].bottom, borderRadius: 30, height: app === 'happ' ? 620 : undefined, minHeight: 300, overflow: 'hidden', position: 'relative', fontFamily: font, opacity: data ? 1 : 0.5,
            }}
          >
            {app === 'happ' ? (
              <div style={{ height: '100%', overflowY: 'auto', paddingBottom: 16, scrollbarWidth: 'none' }}>
                {data && <HappPhone p={data} s={sub} h={happ} dark={theme === 'dark'} />}
              </div>
            ) : (
              <Scaled width={INCY_PHONE_WIDTH}>{data && <IncyScreen p={data} s={sub} c={incyPhone[theme]} />}</Scaled>
            )}
            <div aria-hidden="true" style={{ position: 'absolute', top: 8, left: '50%', transform: 'translateX(-50%)', width: 12, height: 12, borderRadius: 6, background: '#000' }} />
          </div>
        </div>
      )}

      {error && <p className="text-xs text-danger" role="alert">{error}</p>}
      <p className="text-xs text-dim">
        {app === 'happ'
          ? 'Вид Happ нарисован по скриншотам приложения (Android и Windows 4.3.0). Тексты, цвета, баннеры и состояния берутся из формы и обновляются сразу, без сохранения.'
          : (desktop ? 'Вид INCY для компьютера нарисован по скриншоту приложения (3.9.0, тёмная тема), на телефоне (iOS 2.7.0) тоже; светлая тема подобрана на глаз. Тексты,' : 'Вид INCY на телефоне нарисован по скриншоту приложения (iOS 2.7.0, тёмная тема); светлая тема подобрана на глаз. Тексты,')
            + ' цвета, баннеры и состояния берутся из формы и обновляются сразу, без сохранения.'}
      </p>
    </div>
  )
}
