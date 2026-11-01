import { useState } from 'react'
import { api, ApiError } from '../api'
import { Alert, Badge, Button, Card, Field, Input, Modal, PageHeader, Textarea } from '../components/ui'
import { ask } from '../components/confirm'
import { useLoad } from '../hooks'
import type { ExternalNode, ImportResult } from '../types'

const errText = (e: unknown) => (e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e))

function AddModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<ImportResult | null>(null)

  async function submit() {
    setBusy(true)
    setError('')
    try {
      const r = await api<ImportResult>('/api/external-nodes/import', { method: 'POST', body: { text } })
      setResult(r)
      if (r.added > 0) onDone()
      if (r.problems.length === 0) onClose()
    } catch (e) {
      setError(errText(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title="Добавить внешние ноды" onClose={onClose} wide>
      <p className="mb-3 text-sm text-dim">
        Вставьте ссылки <code className="rounded bg-surface-2 px-1">vless://</code> или <code className="rounded bg-surface-2 px-1">trojan://</code>, по одной в строке,
        или содержимое чужой подписки. Название берётся из ссылки (часть после #), его можно потом изменить.
      </p>
      <Field label="Ссылки">
        <Textarea rows={8} value={text} onChange={(e) => setText(e.target.value)} placeholder="vless://…#Нидерланды" spellCheck={false} />
      </Field>
      {error && <div className="mt-3"><Alert>{error}</Alert></div>}
      {result && (
        <div className="mt-3 grid gap-2">
          {result.added > 0 && <Alert tone="ok">Добавлено: {result.added}</Alert>}
          {result.problems.length > 0 && (
            <Alert tone="warn">
              <div>Не добавлено:</div>
              <ul className="mt-1 list-disc pl-5">
                {result.problems.map((p) => <li key={p.line}>строка {p.line}: {p.error}</li>)}
              </ul>
            </Alert>
          )}
        </div>
      )}
      <div className="mt-4 flex justify-end gap-2">
        <Button variant="ghost" onClick={onClose}>Закрыть</Button>
        <Button variant="primary" onClick={submit} disabled={busy || !text.trim()}>{busy ? 'Добавляем…' : 'Добавить'}</Button>
      </div>
    </Modal>
  )
}

function Row({ n, onChanged }: { n: ExternalNode; onChanged: () => void }) {
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(n.name)
  const [error, setError] = useState('')

  async function put(body: Partial<ExternalNode>) {
    setError('')
    try {
      await api(`/api/external-nodes/${n.id}`, { method: 'PUT', body: { name: n.name, enable: n.enable, ...body } })
      setEditing(false)
      onChanged()
    } catch (e) {
      setError(errText(e))
    }
  }

  async function remove() {
    if (!(await ask(`Удалить «${n.name}»?`, { confirmLabel: 'Удалить' }))) return
    try {
      await api(`/api/external-nodes/${n.id}`, { method: 'DELETE' })
      onChanged()
    } catch (e) {
      setError(errText(e))
    }
  }

  return (
    <li className="py-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          {editing ? (
            <div className="flex items-center gap-2">
              <Input value={name} onChange={(e) => setName(e.target.value)} aria-label="Название" />
              <Button variant="primary" onClick={() => void put({ name })}>OK</Button>
              <Button variant="ghost" onClick={() => { setEditing(false); setName(n.name) }}>Отмена</Button>
            </div>
          ) : (
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{n.name}</span>
              {n.enable ? <Badge tone="ok">в подписках</Badge> : <Badge>выключена</Badge>}
            </div>
          )}
          <div className="mt-0.5 text-xs text-dim">
            {n.protocol} · {n.host}:{n.port} · {n.network} · {n.security}
          </div>
        </div>
        {!editing && (
          <div className="flex gap-1">
            <Button variant="ghost" onClick={() => void put({ enable: !n.enable })}>{n.enable ? 'Выключить' : 'Включить'}</Button>
            <Button variant="ghost" onClick={() => setEditing(true)}>Переименовать</Button>
            <Button variant="danger" onClick={() => void remove()}>Удалить</Button>
          </div>
        )}
      </div>
      {error && <div className="mt-2"><Alert>{error}</Alert></div>}
    </li>
  )
}

export default function ExternalNodes() {
  const nodes = useLoad<ExternalNode[]>('/api/external-nodes')
  const [adding, setAdding] = useState(false)

  return (
    <section className="max-w-3xl">
      <PageHeader title="Внешние ноды" hint="Чужие серверы попадают в подписку каждого клиента рядом с вашими подключениями во всех форматах. Клиентам с истёкшим сроком, исчерпанным трафиком или выключенным они не выдаются.">
        <Button variant="primary" onClick={() => setAdding(true)}>Добавить</Button>
      </PageHeader>      {nodes.error && <Alert>{nodes.error}</Alert>}
      <Card className="px-5">
        {nodes.data && nodes.data.length === 0 && <p className="py-4 text-sm text-dim">Внешних нод пока нет.</p>}
        {nodes.data && nodes.data.length > 0 && (
          <ul className="divide-y divide-line">
            {nodes.data.map((n) => <Row key={n.id} n={n} onChanged={() => void nodes.reload()} />)}
          </ul>
        )}
      </Card>
      {adding && <AddModal onClose={() => setAdding(false)} onDone={() => void nodes.reload()} />}
    </section>
  )
}
