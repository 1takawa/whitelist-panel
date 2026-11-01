import { useState } from 'react'
import { api } from '../../api'
import { Alert, Button, Field, Input, Select } from '../../components/ui'
import { withEncMode } from './doc'
import { Full, Grid, Section, useForm } from './form'

type Pair = { decryption: string; encryption: string }
type EncKeys = { x25519: Pair; mlkem768: Pair }

const methods: Array<[string, string]> = [
  ['x25519:native', 'X25519 (native)'],
  ['x25519:xorpub', 'X25519 (xorpub)'],
  ['x25519:random', 'X25519 (random)'],
  ['mlkem768:native', 'ML-KEM-768 (native)'],
  ['mlkem768:xorpub', 'ML-KEM-768 (xorpub)'],
  ['mlkem768:random', 'ML-KEM-768 (random)'],
]

type Fallback = { name?: string; alpn?: string; path?: string; dest?: string | number; xver?: number }

function VlessEncryption() {
  const { d, set } = useForm()
  const [method, setMethod] = useState('x25519:native')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const dec = String(d.settings.decryption ?? 'none')
  const enc = String(d.settings.encryption ?? '')

  async function generate() {
    setBusy(true)
    setErr('')
    try {
      const keys = await api<EncKeys>('/api/xray/vlessenc', { method: 'POST' })
      const [kind, mode] = method.split(':')
      const pair = kind === 'x25519' ? keys.x25519 : keys.mlkem768
      set('settings.decryption', withEncMode(pair.decryption, mode))
      set('settings.encryption', withEncMode(pair.encryption, mode))
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Section title="Шифрование VLESS (Decryption / Encryption)" hint="Необязательное постквантовое шифрование поверх любого транспорта. Decryption остаётся на сервере, Encryption попадает в ссылки клиентов.">
      <Full>
        <Field label="Decryption" hint='«none» — без шифрования'>
          <Input value={dec} spellCheck={false} className="font-mono text-xs" onChange={(e) => set('settings.decryption', e.target.value || 'none')} />
        </Field>
      </Full>
      <Full>
        <Field label="Encryption" hint="Выдаётся клиентам в ссылке (параметр encryption)">
          <Input value={enc} spellCheck={false} className="font-mono text-xs" onChange={(e) => set('settings.encryption', e.target.value === '' ? undefined : e.target.value)} />
        </Field>
      </Full>
      <Full>
        <div className="flex flex-wrap items-end gap-2">
          <div className="min-w-[14rem]">
            <Field label="Сгенерировать ключи">
              <Select value={method} onChange={(e) => setMethod(e.target.value)}>
                {methods.map(([v, t]) => <option key={v} value={v}>{t}</option>)}
              </Select>
            </Field>
          </div>
          <Button variant="primary" disabled={busy} onClick={() => void generate()}>{busy ? 'Генерируем…' : 'Сгенерировать'}</Button>
          <Button onClick={() => { set('settings.decryption', 'none'); set('settings.encryption', undefined) }}>Очистить</Button>
        </div>
        {err && <div className="mt-2"><Alert>{err}</Alert></div>}
      </Full>
    </Section>
  )
}

function Testseed() {
  const { d, set } = useForm()
  const cur = (d.settings.testseed as number[] | undefined) ?? [900, 500, 900, 256]
  const write = (i: number, v: string) => {
    const next = [...cur]
    next[i] = Math.max(0, Math.trunc(Number(v) || 0))
    set('settings.testseed', next)
  }
  return (
    <Section title="Vision testseed" hint="Применяется только к клиентам с потоком xtls-rprx-vision, иначе игнорируется.">
      <Full>
        <div className="grid grid-cols-4 gap-2">
          {cur.map((v, i) => <Input key={i} type="number" min={0} aria-label={`testseed ${i + 1}`} value={v} onChange={(e) => write(i, e.target.value)} />)}
        </div>
      </Full>
    </Section>
  )
}

// Where a connection goes when it is not a valid proxy request (a web site on the same port, another inbound…).
function Fallbacks() {
  const { d, set, others } = useForm()
  const list = (d.settings.fallbacks as Fallback[] | undefined) ?? []
  const write = (next: Fallback[]) => set('settings.fallbacks', next.length ? next : undefined)
  const change = (i: number, k: keyof Fallback, v: string) => {
    const f: Fallback = { ...list[i] }
    if (k === 'xver') f.xver = v === '' ? undefined : Number(v)
    else if (k === 'dest') f.dest = v === '' ? undefined : /^\d+$/.test(v) ? Number(v) : v
    else f[k] = v === '' ? undefined : v
    write(list.map((x, j) => (j === i ? f : x)))
  }

  // "Add all": one fallback per other inbound of this machine, matched by the path of its transport.
  function addAll() {
    const mine = others.filter((o) => o.id !== d.id && (o.serverId ?? '') === (d.serverId ?? '') && o.enable)
    const next = [...list]
    for (const o of mine) {
      const path = o.network === 'ws' ? o.ws.path : o.network === 'httpupgrade' ? o.httpupgrade.path : o.network === 'xhttp' ? o.xhttp.path : o.network === 'grpc' ? `/${o.grpc.serviceName}` : ''
      if (next.some((f) => f.dest === o.port)) continue
      next.push({ dest: o.port, xver: 0, ...(path ? { path } : {}) })
    }
    write(next)
  }

  return (
    <fieldset className="mt-4 rounded-lg border border-line p-4">
      <legend className="px-1 text-sm font-semibold">Fallbacks</legend>
      <p className="mb-3 text-xs text-dim">Куда уходит соединение, которое не является запросом прокси. Dest — порт, host:port или путь сокета.</p>
      {list.length === 0 && <p className="mb-2 text-sm text-dim">Fallback-ов пока нет.</p>}
      <div className="grid gap-2">
        {list.map((f, i) => (
          <div key={i} className="grid gap-2 rounded-lg border border-line p-2 sm:grid-cols-[1fr_1fr_1fr_1fr_5rem_auto]">
            <Input aria-label="SNI (name)" placeholder="SNI (name)" value={f.name ?? ''} onChange={(e) => change(i, 'name', e.target.value)} />
            <Input aria-label="ALPN" placeholder="ALPN" value={f.alpn ?? ''} onChange={(e) => change(i, 'alpn', e.target.value)} />
            <Input aria-label="Path" placeholder="Path" value={f.path ?? ''} onChange={(e) => change(i, 'path', e.target.value)} />
            <Input aria-label="Dest" placeholder="Dest" value={f.dest ?? ''} onChange={(e) => change(i, 'dest', e.target.value)} />
            <Input aria-label="Xver" type="number" min={0} max={2} placeholder="Xver" value={f.xver ?? ''} onChange={(e) => change(i, 'xver', e.target.value)} />
            <button type="button" aria-label="Удалить fallback" className="px-2 text-dim hover:text-danger" onClick={() => write(list.filter((_, j) => j !== i))}>×</button>
          </div>
        ))}
      </div>
      <div className="mt-3 flex gap-2">
        <Button onClick={() => write([...list, { dest: '', xver: 0 }])}>+ Добавить fallback</Button>
        <Button onClick={addAll} title="Добавить по одному fallback на каждое другое включённое подключение этого сервера">Добавить все</Button>
      </div>
    </fieldset>
  )
}

export default function ProtocolTab() {
  const { d } = useForm()
  const vless = d.protocol === 'vless'
  return (
    <div>
      {vless ? (
        <>
          <VlessEncryption />
          <Testseed />
        </>
      ) : (
        <Grid><p className="text-sm text-dim sm:col-span-2">У Trojan нет собственных настроек протокола: клиенты и пароли задаются на странице клиентов.</p></Grid>
      )}
      <Fallbacks />
    </div>
  )
}
