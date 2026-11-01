import { useState } from 'react'
import { buildLink, emptyProfile, parseStored, toLines, type RoutingProfile } from '../lib/happRouting'
import RoutingPresetPicker from './RoutingPresetPicker'
import { Alert, Button, Field, Input, Select, Textarea, Toggle } from './ui'

const LISTS: { key: 'DirectSites' | 'DirectIp' | 'ProxySites' | 'ProxyIp' | 'BlockSites' | 'BlockIp'; label: string; hint: string }[] = [
  { key: 'DirectSites', label: 'Домены напрямую', hint: 'domain:ru, geosite:private, full:example.com' },
  { key: 'DirectIp', label: 'IP напрямую', hint: 'geoip:ru, 10.0.0.0/8' },
  { key: 'ProxySites', label: 'Домены через прокси', hint: 'domain:example.com' },
  { key: 'ProxyIp', label: 'IP через прокси', hint: 'geoip:us' },
  { key: 'BlockSites', label: 'Блокировать домены', hint: 'geosite:category-ads-all' },
  { key: 'BlockIp', label: 'Блокировать IP', hint: '' },
]

// Edits a Happ routing profile. The stored value is always a happ://routing link (or happ://routing/off); the
// structured form below is a convenience for reading and rebuilding it.
export default function HappRoutingEditor({ enabled, value, onEnabled, onChange }: { enabled: boolean; value: string; onEnabled: (v: boolean) => void; onChange: (v: string) => void }) {
  const [profile, setProfile] = useState<RoutingProfile | null>(null)
  const [note, setNote] = useState<{ tone: 'ok' | 'warn' | 'bad'; text: string } | null>(null)


  function load() {
    const r = parseStored(value)
    if (r.ok) {
      setProfile(r.profile)
      setNote(null)
    } else if (r.reason === 'empty') {
      setProfile(emptyProfile())
      setNote(null)
    } else {
      setNote({ tone: r.reason === 'off' ? 'warn' : 'bad', text: r.reason === 'off' ? 'Сейчас выбрано «выключить маршрутизацию»: редактировать нечего.' : 'Не удалось разобрать значение: оно должно быть ссылкой happ://routing/… или JSON-профилем.' })
    }
  }

  function build() {
    if (!profile) return
    const clean: RoutingProfile = { ...profile, Name: profile.Name.trim() || 'Custom Rules' }
    onChange(buildLink(clean))
    setNote({ tone: 'ok', text: 'Ссылка собрана. Нажмите «Сохранить» внизу страницы.' })
  }

  const set = (patch: Partial<RoutingProfile>) => setProfile((p) => (p ? { ...p, ...patch } : p))

  return (
    <div className="grid gap-4">
      <div>
        <Toggle label="Отправлять маршрутизацию" checked={enabled} onChange={onEnabled} />
        <p className="mt-1 text-xs text-dim">Профиль уходит в заголовке Routing всем приложениям, кроме INCY (оно получает свой профиль из вкладки Incy).</p>
      </div>

      <RoutingPresetPicker
        app="happ"
        current={value}
        extra={[{ value: 'off', label: 'Выключить маршрутизацию в приложении', link: 'happ://routing/off' }]}
        onPick={(link) => { onChange(link); setProfile(null); setNote(null) }}
      />

      <Field label="Значение" hint="Ссылка happ://routing/onadd/… , happ://routing/off или JSON-профиль (панель сама превратит его в ссылку).">
        <Textarea rows={3} value={value} onChange={(e) => { onChange(e.target.value); setNote(null) }} spellCheck={false} />
      </Field>

      {note && <Alert tone={note.tone}>{note.text}</Alert>}

      <details className="rounded-lg border border-line p-3" onToggle={(e) => { if ((e.currentTarget as HTMLDetailsElement).open && !profile) load() }}>
        <summary className="cursor-pointer text-sm font-medium">Редактор правил</summary>
        {profile && (
          <div className="mt-3 grid gap-3">
            <div className="grid gap-3 sm:grid-cols-3">
              <Field label="Название профиля"><Input value={profile.Name} onChange={(e) => set({ Name: e.target.value })} /></Field>
              <Field label="Режим">
                <Select value={profile.GlobalProxy} onChange={(e) => set({ GlobalProxy: e.target.value as 'true' | 'false' })}>
                  <option value="true">Глобальный прокси</option>
                  <option value="false">Только правила</option>
                </Select>
              </Field>
              <Field label="Стратегия доменов">
                <Select value={profile.DomainStrategy} onChange={(e) => set({ DomainStrategy: e.target.value })}>
                  <option value="AsIs">AsIs</option>
                  <option value="IPIfNonMatch">IPIfNonMatch</option>
                  <option value="IPOnDemand">IPOnDemand</option>
                </Select>
              </Field>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              {LISTS.map((l) => (
                <Field key={l.key} label={l.label} hint={l.hint ? `По одному правилу в строке. Например: ${l.hint}` : 'По одному правилу в строке'}>
                  <Textarea rows={4} value={profile[l.key].join('\n')} onChange={(e) => set({ [l.key]: toLines(e.target.value) })} spellCheck={false} />
                </Field>
              ))}
            </div>
            <div><Button onClick={build}>Собрать ссылку</Button></div>
          </div>
        )}
      </details>
    </div>
  )
}
