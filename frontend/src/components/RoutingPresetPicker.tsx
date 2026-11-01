import { useEffect, useRef, useState } from 'react'
import { api, ApiError } from '../api'
import { useLoad } from '../hooks'
import { formatDate } from '../lib'
import { decodeRoutingLink, geoVersion } from '../lib/happRouting'
import type { RoutingPreset } from '../types'
import { Alert, Button, Field, Select } from './ui'

const CUSTOM = '__custom'

// Picks one of the RoscomVPN routing profiles (github.com/hydraponique/roscomvpn-routing) and keeps the stored one up to
// date. The profile carries its own geo files and DNS settings, so it works as published: nothing is rebuilt here, the
// link goes into the setting as is. The page says nothing while the stored profile is current; when GitHub has a newer
// version an orange button offers the update, and a green note confirms that it was applied.
export default function RoutingPresetPicker({ app, current, onPick, extra }: { app: 'happ' | 'incy'; current: string; onPick: (link: string, preset: RoutingPreset | null) => void; extra?: { value: string; label: string; link: string }[] }) {
  const list = useLoad<RoutingPreset[]>(`/api/routing-presets?app=${app}`)
  const [fresh, setFresh] = useState<RoutingPreset[] | null>(null) // the answer of the last check against GitHub
  const [done, setDone] = useState<{ preset: RoutingPreset } | null>(null) // the profile an update just put into the setting
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [manual, setManual] = useState(false) // the admin pressed the check button: say so when there is nothing to update
  const mounted = useRef(true)
  useEffect(() => () => { mounted.current = false }, [])

  const presets = fresh ?? list.data

  // Looks at the repository again. On opening the page it runs by itself, quietly: a failure is not worth a message then.
  async function recheck(silent: boolean) {
    setBusy(true)
    setError('')
    try {
      const r = await api<RoutingPreset[]>(`/api/routing-presets?app=${app}&refresh=1`)
      if (mounted.current) {
        setFresh(r)
        setManual(!silent)
      }
    } catch (e) {
      if (mounted.current && !silent) setError(e instanceof ApiError ? e.message : String(e))
    } finally {
      if (mounted.current) setBusy(false)
    }
  }
  useEffect(() => { void recheck(true) }, []) // eslint-disable-line react-hooks/exhaustive-deps

  function pick(id: string) {
    const extraItem = extra?.find((x) => x.value === id)
    if (extraItem) {
      setDone(null)
      onPick(extraItem.link, null)
      return
    }
    const p = presets?.find((x) => x.id === id)
    if (!p) return
    setDone(null) // choosing from the list needs no confirmation: the select shows the choice
    onPick(p.link, p)
  }

  const cur = current.trim()
  const have = cur ? decodeRoutingLink(cur) : null
  const byLink = presets?.find((x) => x.link === cur)
  const byName = have ? presets?.find((x) => x.name === have.name) : undefined
  // The select shows the stored profile (also when it is an older version of a listed one), so it never says "choose" over a chosen profile.
  const selected = !cur ? '' : (extra?.find((x) => x.link.trim() === cur)?.value ?? byLink?.id ?? byName?.id ?? (presets ? CUSTOM : ''))
  const newer = byName && !byLink ? byName : null // GitHub has another version of the stored profile

  const geoNote = (p: RoutingPreset) => {
    const theirs = decodeRoutingLink(p.link)
    if (!have || !theirs) return ''
    const parts = [
      ['geoip', have.geoip, theirs.geoip],
      ['geosite', have.geosite, theirs.geosite],
    ]
      .filter(([, a, b]) => a !== b)
      .map(([n, a, b]) => `${n} ${geoVersion(a) || '—'} → ${geoVersion(b) || '—'}`)
    return parts.length ? ` Изменились файлы: ${parts.join(', ')}.` : ''
  }
  const updateHint = (p: RoutingPreset) =>
    `Версия на GitHub${p.updated ? ` от ${formatDate(p.updated)}` : ''}, сохранённая — от ${have?.updated ? formatDate(have.updated.toISOString()) : 'неизвестной даты'}.${geoNote(p)}${p.source === 'embedded' ? ' GitHub сейчас недоступен: сравнение со встроенной копией.' : ''}`

  return (
    <div className="grid gap-2">
      <div className="grid gap-3 sm:grid-cols-[1fr_auto] sm:items-end">
        <Field label="Готовый профиль" hint="Профили RoscomVPN: свои geo-файлы и DNS уже внутри, правила подставлять не нужно.">
          <Select value={selected} onChange={(e) => pick(e.target.value)} disabled={!presets}>
            <option value="">{presets ? '— выбрать —' : 'Загрузка…'}</option>
            {presets?.map((p) => <option key={p.id} value={p.id} title={p.hint}>{p.label}</option>)}
            {extra?.map((x) => <option key={x.value} value={x.value}>{x.label}</option>)}
            {selected === CUSTOM && <option value={CUSTOM} disabled>Свой профиль (не из списка)</option>}
          </Select>
        </Field>
        <div className="flex flex-wrap items-center gap-2">
          {newer && (
            <Button variant="warn" title={updateHint(newer)} onClick={() => { setDone({ preset: newer }); onPick(newer.link, newer) }}>
              Обновить профиль
            </Button>
          )}
          <Button variant="ghost" onClick={() => void recheck(false)} disabled={busy} title="Заново прочитать репозиторий RoscomVPN и сравнить с сохранённым профилем">
            {busy ? 'Проверяем…' : 'Проверить обновление'}
          </Button>
        </div>
      </div>

      {list.error && <Alert>{list.error}</Alert>}
      {error && <Alert>{error}</Alert>}
      {manual && !busy && !newer && !error && selected !== '' && selected !== CUSTOM && !done && <div className="text-xs text-emerald-600 dark:text-emerald-400">Актуальная версия.</div>}
      {done && cur === done.preset.link && (
        <Alert tone="ok">
          Профиль обновлён: «{done.preset.name}»{done.preset.updated ? `, версия от ${formatDate(done.preset.updated)}` : ''}. Нажмите «Сохранить» внизу страницы, чтобы он попал в подписку.
        </Alert>
      )}
    </div>
  )
}
