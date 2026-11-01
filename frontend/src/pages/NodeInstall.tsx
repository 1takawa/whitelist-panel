import { useEffect, useRef, useState } from 'react'
import { api, ApiError } from '../api'
import { Alert, Button, Field, Input, Modal, Textarea, Toggle } from '../components/ui'
import { DomainPicker } from '../components/DomainPicker'
import CountryPicker from '../components/CountryPicker'
import type { NodeJob, SshReport } from '../types'

const errText = (e: unknown) => (e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e))

// JobLog follows a background job (installation or upgrade) and shows its log until it ends.
export function JobLog({ jobId, onEnd }: { jobId: string; onEnd: (j: NodeJob) => void }) {
  const [job, setJob] = useState<NodeJob | null>(null)
  const [lost, setLost] = useState('')
  const box = useRef<HTMLPreElement>(null)
  const onEndRef = useRef(onEnd)
  onEndRef.current = onEnd

  useEffect(() => {
    let stop = false
    let timer: ReturnType<typeof setTimeout>
    const tick = async () => {
      try {
        const j = await api<NodeJob>(`/api/server-jobs/${jobId}`)
        if (stop) return
        setJob(j)
        if (j.status !== 'running') {
          onEndRef.current(j)
          return
        }
      } catch (e) {
        if (stop) return
        setLost(errText(e))
      }
      timer = setTimeout(() => void tick(), 1000)
    }
    void tick()
    return () => {
      stop = true
      clearTimeout(timer)
    }
  }, [jobId])

  useEffect(() => {
    box.current?.scrollTo({ top: box.current.scrollHeight })
  }, [job?.log.length])

  return (
    <div className="grid gap-3">
      <pre ref={box} className="max-h-72 min-h-40 overflow-auto rounded-lg bg-surface-2 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap" aria-live="polite">
        {job ? job.log.join('\n') || 'Запуск…' : 'Запуск…'}
      </pre>
      {job?.status === 'running' && <p className="text-sm text-dim">Идёт установка, окно закрывать не нужно…</p>}
      {job?.status === 'done' && <Alert tone="ok">Готово.</Alert>}
      {job?.status === 'failed' && <Alert>{job.error ?? 'Не удалось.'}</Alert>}
      {job?.warnings.map((w) => <Alert key={w} tone="warn">{w}</Alert>)}
      {lost && <Alert tone="warn">Связь с панелью потеряна: {lost}. Состояние можно посмотреть в списке серверов.</Alert>}
    </div>
  )
}

type Form = {
  host: string
  sshPort: string
  user: string
  password: string
  privateKey: string
  useKey: boolean
  realityDomain: string
  name: string
  country: string
  agentPort: string
  createInbound: boolean
  addClients: boolean
  forcePort: boolean
}

const empty: Form = {
  host: '', sshPort: '22', user: 'root', password: '', privateKey: '', useKey: false, realityDomain: '',
  name: '', country: '', agentPort: '8765', createInbound: true, addClients: true, forcePort: false,
}

// Installer is the "automatic node installation" dialog: SSH access and the node's domain first, then a check, then the
// node's settings. The node gets the nginx-SNI scheme: nginx on 443 by SNI, a certificate and a decoy site for the
// domain, and a REALITY connection behind them.
export function NodeInstaller({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [f, setF] = useState<Form>(empty)
  const [report, setReport] = useState<SshReport | null>(null)
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState('')
  const [checkError, setCheckError] = useState('')
  const [jobId, setJobId] = useState('')
  const [finished, setFinished] = useState<NodeJob | null>(null)
  const [starting, setStarting] = useState(false)

  const set = (patch: Partial<Form>, access = false) => {
    setF((x) => ({ ...x, ...patch }))
    if (access) setReport(null) // a changed address, password or domain needs a new check
  }
  const access = () => ({ host: f.host.trim(), port: Number(f.sshPort), user: f.user.trim(), password: f.useKey ? '' : f.password, privateKey: f.useKey ? f.privateKey : '' })
  const domain = f.createInbound ? f.realityDomain.trim().toLowerCase() : ''
  const canCheck = f.host.trim() !== '' && (f.useKey ? f.privateKey.trim() !== '' : f.password !== '') && (!f.createInbound || domain !== '') && !checking

  async function check() {
    setChecking(true)
    setCheckError('')
    setReport(null)
    try {
      setReport(await api<SshReport>('/api/servers/ssh-check', { method: 'POST', body: { ...access(), agentPort: Number(f.agentPort), connectPort: 443, realityDomain: domain } }))
    } catch (e) {
      setCheckError(errText(e))
    } finally {
      setChecking(false)
    }
  }

  const busy = report?.busyPorts ?? []

  async function install() {
    if (!report?.ok) return
    setStarting(true)
    setError('')
    try {
      const j = await api<NodeJob>('/api/servers/install', {
        method: 'POST',
        body: {
          ...access(),
          hostKey: report.hostKey,
          name: f.name.trim(),
          country: f.country,
          realityDomain: domain,
          agentPort: Number(f.agentPort),
          connectPort: 443,
          createInbound: f.createInbound,
          addClients: f.createInbound && f.addClients,
          forcePort: f.createInbound && busy.length > 0 && f.forcePort,
        },
      })
      setFinished(null)
      setJobId(j.id)
    } catch (e) {
      setError(errText(e))
    } finally {
      setStarting(false)
    }
  }

  const installing = jobId !== ''
  const missing = [f.name.trim() === '' && 'имя в подписке'].filter(Boolean)
  const ready = report?.ok && missing.length === 0

  return (
    <Modal title="Автоматическая установка ноды" onClose={() => { if (finished?.status === 'done') onDone(); onClose() }} wide persistent>
      {installing ? (
        <div className="grid gap-4">
          <JobLog jobId={jobId} onEnd={(j) => { setFinished(j); if (j.status === 'done') onDone() }} />
          <div className="flex flex-wrap items-center justify-end gap-2">
            {finished?.status === 'failed' && <Button variant="ghost" onClick={() => { setJobId(''); setFinished(null) }}>Назад к настройкам</Button>}
            <Button variant="primary" disabled={!finished} onClick={onClose}>{finished ? 'Закрыть' : 'Установка…'}</Button>
          </div>
        </div>
      ) : (
        <form className="grid gap-5" onSubmit={(e) => { e.preventDefault(); if (!report?.ok) { if (canCheck) void check() } else if (ready && !starting) void install() }}>
          <p className="text-sm text-dim">
            На новый сервер ставятся Xray и агент без панели и базы, а также, по схеме с nginx, nginx с маршрутизацией по SNI на порту 443, сертификат Let&apos;s Encrypt
            и сайт-заглушка для домена ноды. Панель сама подключается по SSH, создаёт ключ для дальнейшего обслуживания и связывает ноду с собой.
            SSH-пароль используется только во время установки и нигде не сохраняется.
          </p>

          <fieldset className="grid gap-3 rounded-xl border border-line p-4">
            <legend className="px-2 text-sm font-medium">Доступ к серверу</legend>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="SSH-адрес сервера"><Input value={f.host} onChange={(e) => set({ host: e.target.value }, true)} placeholder="203.0.113.20" autoFocus spellCheck={false} /></Field>
              <Field label="SSH-порт"><Input type="number" min={1} max={65535} value={f.sshPort} onChange={(e) => set({ sshPort: e.target.value }, true)} /></Field>
              <Field label="Пользователь"><Input value={f.user} onChange={(e) => set({ user: e.target.value }, true)} autoComplete="off" /></Field>
              {f.useKey ? (
                <div className="sm:col-span-2">
                  <Field label="Приватный ключ (PEM)" hint="Используется только во время установки. Ключ с парольной фразой не поддерживается.">
                    <Textarea value={f.privateKey} onChange={(e) => set({ privateKey: e.target.value }, true)} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" spellCheck={false} autoComplete="off" />
                  </Field>
                </div>
              ) : (
                <Field label="Пароль root"><Input type="password" value={f.password} onChange={(e) => set({ password: e.target.value }, true)} autoComplete="new-password" /></Field>
              )}
            </div>
            <Toggle checked={f.useKey} onChange={(v) => set({ useKey: v }, true)} label="Войти по приватному ключу вместо пароля" />
            {f.createInbound && (
              <Field label="Домен ноды для REALITY" hint="Свой домен или поддомен с A-записью на адрес этого сервера: на нём выпускается сертификат и ставится сайт-заглушка, это же имя (SNI) получают клиенты.">
                <DomainPicker
                  value={f.realityDomain}
                  onChange={(v) => set({ realityDomain: v }, true)}
                  placeholder="reality.example.com"
                  target={{ host: f.host.trim() }}
                  targetReady={f.host.trim() !== ''}
                />
              </Field>
            )}
            <div className="flex flex-wrap items-center justify-between gap-3">
              <span className="text-xs text-dim">Нужен вход под root, Ubuntu 22.04/24.04 или Debian 12, x86_64.</span>
              <Button variant={report ? 'default' : 'primary'} disabled={!canCheck} onClick={() => void check()}>{checking ? 'Проверяем…' : 'Проверить'}</Button>
            </div>
            {checkError && <Alert>{checkError}</Alert>}
            {report && (
              <div className="grid gap-2">
                {report.ok ? (
                  <Alert tone="ok">
                    Вход выполнен: {report.os || 'система определена'}, {report.arch}. Отпечаток ключа сервера: <code className="font-mono text-xs">{report.hostKey.slice(0, 16)}…</code>
                    {' '}Дальше панель будет подключаться только к серверу с этим ключом.
                    {report.domainIp && <> Домен {domain} указывает на {report.domainIp}: сертификат будет выпущен.</>}
                  </Alert>
                ) : (
                  <Alert>Установить нельзя:<ul className="mt-1 list-disc pl-5">{report.problems.map((p) => <li key={p}>{p}</li>)}</ul></Alert>
                )}
                {report.warnings.map((w) => <Alert key={w} tone="warn">{w}</Alert>)}
              </div>
            )}
          </fieldset>

          <fieldset className={`grid gap-3 rounded-xl border border-line p-4 ${report?.ok ? '' : 'opacity-50'}`} disabled={!report?.ok}>
            <legend className="px-2 text-sm font-medium">Настройки ноды</legend>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Имя в подписке" hint="Так нода называется в панели и в списке серверов приложения клиента.">
                <Input value={f.name} onChange={(e) => set({ name: e.target.value })} placeholder="Франкфурт" />
              </Field>
              <Field label="Страна и флаг">
                <CountryPicker value={f.country} onChange={(country) => set({ country })} emptyLabel="Определить автоматически" />
              </Field>
              <Field label="Порт агента" hint="Его нужно открыть для главного сервера."><Input type="number" min={1} max={65535} value={f.agentPort} onChange={(e) => set({ agentPort: e.target.value })} /></Field>
            </div>
            <Toggle checked={f.createInbound} onChange={(v) => set({ createInbound: v }, true)} label="Сразу создать подключение VLESS + REALITY на этой ноде (с nginx и сайтом-заглушкой)" />
            <Toggle checked={f.createInbound && f.addClients} onChange={(v) => set({ addClients: v })} label="Добавить в него всех существующих клиентов" />
            {f.createInbound && busy.length > 0 && (
              <div className="grid gap-2 rounded-lg border border-amber-500/40 p-3">
                <p className="text-sm text-amber-600 dark:text-amber-400">
                  Нужные порты заняты: {busy.map((b) => `${b.port}${b.holder ? ` (${b.holder})` : ''}`).join(', ')}. Без освобождения установка остановится.
                </p>
                <Toggle checked={f.forcePort} onChange={(v) => set({ forcePort: v })} label="Освободить порты 80 и 443 принудительно" />
                {f.forcePort && (
                  <p className="text-xs text-dim">
                    Перед установкой панель остановит программы, которые слушают порты 80 и 443, и отключит их автозапуск (если это службы systemd); процессы не из служб будут завершены.
                    Сайты и сервисы на них перестанут работать. SSH это не затрагивает: порт SSH освободить нельзя.
                  </p>
                )}
              </div>
            )}
          </fieldset>

          {error && <Alert>{error}</Alert>}
          <div className="flex flex-wrap items-center justify-end gap-2">
            {report?.ok && missing.length > 0 && <span className="mr-auto text-sm text-amber-600 dark:text-amber-400">Заполните: {missing.join(', ')}</span>}
            <Button variant="ghost" onClick={onClose}>Отмена</Button>
            <Button variant="primary" disabled={!ready || starting} onClick={() => void install()}>{starting ? 'Запускаем…' : 'Установить ноду'}</Button>
          </div>
        </form>
      )}
    </Modal>
  )
}
