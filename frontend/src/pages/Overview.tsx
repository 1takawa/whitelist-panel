import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { api } from '../api'
import { Icon } from '../components/Icon'
import { Alert, Button, Card, PageHeader, Segmented } from '../components/ui'
import { useLoad } from '../hooks'
import { formatBytes, formatUptime } from '../lib'
import type { Dashboard } from '../types'

// Counts up from the previous value, so a refresh nudges the number instead of snapping it.
function useCountUp(to: number, delay = 0) {
  const [v, setV] = useState(0)
  const from = useRef(0)
  useEffect(() => {
    if (matchMedia('(prefers-reduced-motion: reduce)').matches) {
      setV(to)
      from.current = to
      return
    }
    const start = from.current
    let raf = 0
    const t0 = performance.now() + delay
    const tick = (t: number) => {
      const k = Math.min(1, Math.max(0, (t - t0) / 900))
      setV(Math.round(start + (to - start) * (1 - Math.pow(1 - k, 4))))
      if (k < 1) raf = requestAnimationFrame(tick)
      else from.current = to
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [to, delay])
  return v
}

type Tile = { id: string; label: string; n: number; tone: string; filter: string }

function StatTile({ t, i, onOpen }: { t: Tile; i: number; onOpen: () => void }) {
  const v = useCountUp(t.n, i * 50)
  return (
    <button
      type="button"
      onClick={onOpen}
      style={{ '--c': t.tone } as React.CSSProperties}
      className="rounded-2xl border border-line bg-surface p-4 text-left shadow-card transition-[transform,border-color] duration-[250ms] ease-[var(--ease)] hover:-translate-y-0.5 hover:border-[color-mix(in_srgb,var(--c)_40%,var(--border))]"
    >
      <div className="flex items-center gap-2 text-[12.5px] font-medium text-dim">
        <i className="size-[7px] rounded-full bg-[var(--c)]" />
        {t.label}
      </div>
      <div className="mt-2.5 text-[32px] font-bold leading-none tracking-[-0.035em] tabular-nums">{v}</div>
    </button>
  )
}

function Meter({ label, value, text, delay = 0 }: { label: string; value: number; text: string; delay?: number }) {
  const pct = Math.max(0, Math.min(100, value))
  const tone = pct >= 90 ? 'bg-blocked' : pct >= 70 ? 'bg-expired' : 'bg-brand'
  return (
    <div>
      <div className="flex items-baseline justify-between gap-3 text-[12.5px]">
        <span className="min-w-0 truncate text-dim">{label}</span>
        <span className="shrink-0 tabular-nums">{text}</span>
      </div>
      <div className="mt-1.5 h-[5px] overflow-hidden rounded-full bg-surface-2" role="progressbar" aria-label={label} aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
        <div className={`ap-grow-x h-full rounded-full ${tone}`} style={{ width: `${pct}%`, animationDelay: `${delay}ms` }} />
      </div>
    </div>
  )
}

const clock = (unix: number) => new Date(unix * 1000).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })

// Traffic of the last 24 hours: download below, upload above. Hovering a bar names the busiest nodes of that hour.
function HistoryChart({ points }: { points: Dashboard['history'] }) {
  const [hover, setHover] = useState<number | null>(null)
  const max = Math.max(1, ...points.map((p) => p.up + p.down))
  const hp = hover === null ? null : points[hover]
  const top = hp?.nodes[0]?.bytes ?? 1
  return (
    <div className="relative mt-4" onPointerLeave={() => setHover(null)}>
      {hp && (
        <div
          className="glass pointer-events-none absolute bottom-[calc(100%-8px)] z-20 w-[236px] -translate-x-1/2 rounded-[14px] border border-line-strong p-3 shadow-pop"
          style={{ left: `clamp(118px, ${(((hover ?? 0) + 0.5) / points.length) * 100}%, calc(100% - 118px))` }}
        >
          <div className="flex items-baseline justify-between">
            <b className="text-[12.5px] font-semibold tabular-nums">{clock(hp.time)}</b>
            <span className="text-xs text-dim tabular-nums">↓ {formatBytes(hp.down)} · ↑ {formatBytes(hp.up)}</span>
          </div>
          {hp.nodes.length > 0 ? (
            <>
              <div className="mt-2.5 text-[11px] uppercase tracking-wider text-faint">Самые активные ноды</div>
              {hp.nodes.map((n) => (
                <div key={n.name} className="mt-2">
                  <div className="flex justify-between gap-2 text-xs">
                    <span className="min-w-0 truncate">{n.name}</span>
                    <span className="shrink-0 text-dim tabular-nums">{formatBytes(n.bytes)}</span>
                  </div>
                  <div className="mt-1 h-1 overflow-hidden rounded-full bg-surface-2"><div className="h-full rounded-full bg-brand" style={{ width: `${(n.bytes / top) * 100}%` }} /></div>
                </div>
              ))}
            </>
          ) : (
            <div className="mt-2 text-xs text-faint">{hp.up + hp.down > 0 ? 'Разбивки по нодам нет' : 'Трафика не было'}</div>
          )}
        </div>
      )}
      <div className="group flex h-[190px] items-end gap-[5px] border-b border-line" role="img" aria-label="Трафик за последние 24 часа">
        {points.map((p, i) => (
          <div key={p.time} onPointerEnter={() => setHover(i)} className="peer flex h-full flex-1 cursor-pointer flex-col justify-end gap-0.5">
            <i className={`ap-rise block rounded-t-[4px] rounded-b-[2px] bg-brand/40 transition-opacity ${hover !== null && hover !== i ? 'opacity-35' : ''}`} style={{ height: `${((p.up) / max) * 100}%`, animationDelay: `${i * 18}ms` }} />
            <i className={`ap-rise block rounded-t-[4px] rounded-b-[2px] bg-brand transition-opacity ${hover !== null && hover !== i ? 'opacity-35' : ''}`} style={{ height: `${(p.down / max) * 100}%`, animationDelay: `${i * 18}ms` }} />
          </div>
        ))}
      </div>
      <div className="mt-2 flex justify-between text-[11px] text-faint tabular-nums">
        {[0, 6, 12, 18].map((i) => <span key={i}>{points[i] ? clock(points[i].time) : ''}</span>)}
        <span>сейчас</span>
      </div>
    </div>
  )
}

export default function Overview() {
  const dash = useLoad<Dashboard>('/api/dashboard')
  const logs = useLoad<{ lines: string[] }>('/api/xray/logs?n=100')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [logOpen, setLogOpen] = useState(false)
  const [period, setPeriod] = useState<'day' | 'week' | 'all'>('all')
  const nav = useNavigate()

  const reloadDash = dash.reload
  useEffect(() => {
    const t = setInterval(() => void reloadDash(), 5000)
    return () => clearInterval(t)
  }, [reloadDash])

  async function restart() {
    setBusy(true)
    setError('')
    try {
      await api('/api/xray/restart', { method: 'POST' })
      await Promise.all([dash.reload(), logs.reload()])
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const d = dash.data
  const st = d?.xray
  const sys = d?.system
  const hasSys = !!sys && sys.memTotal > 0

  const c = d?.clients
  const top = d ? (d.topPeriods?.[period] ?? d.top).slice(0, 5) : []
  const expired = c ? c.expired + c.depleted : 0
  const tiles: Tile[] = c
    ? [
        { id: 'all', label: 'Всего', n: c.total, tone: 'var(--text)', filter: 'all' },
        { id: 'online', label: 'Онлайн', n: c.online, tone: 'var(--st-online)', filter: 'online' },
        { id: 'active', label: 'Активны', n: c.active, tone: 'var(--st-active)', filter: 'active' },
        { id: 'trial', label: 'Тестовые', n: c.trial, tone: 'var(--st-trial)', filter: 'trial' },
        { id: 'expired', label: 'Истекли', n: expired, tone: 'var(--st-expired)', filter: 'expired' },
        { id: 'blocked', label: 'Заблокированы', n: c.disabled, tone: 'var(--st-blocked)', filter: 'blocked' },
      ]
    : []
  const mix = c
    ? [
        { label: 'Активны', n: c.active - c.trial, tone: 'var(--st-active)' },
        { label: 'Тестовые', n: c.trial, tone: 'var(--st-trial)' },
        { label: 'Истекли', n: expired, tone: 'var(--st-expired)' },
        { label: 'Заблокированы', n: c.disabled, tone: 'var(--st-blocked)' },
      ]
    : []

  return (
    <section>
      <PageHeader title="Обзор">
        {st && (
          <span className="inline-flex h-[30px] items-center gap-2 rounded-full border border-line bg-surface px-3 text-[12.5px] font-medium">
            <i className={`size-[7px] rounded-full ${st.running ? 'ap-pulse bg-online [--c:var(--st-online)]' : 'bg-blocked'}`} />
            {st.running ? `Xray${st.version ? ` ${st.version}` : ''} · ${formatUptime(st.uptimeSeconds)}` : st.installed ? 'Xray остановлен' : 'Xray не установлен'}
          </span>
        )}
        <Button onClick={restart} disabled={busy || !st?.installed}>
          <Icon name="refresh" className={`size-4 ${busy ? 'animate-spin' : ''}`} />
          Перезапуск
        </Button>
      </PageHeader>

      <div className="mb-4 grid gap-2">
        {(error || dash.error) && <Alert>{error || dash.error}</Alert>}
        {st && !st.installed && <Alert tone="warn">Xray не установлен: подключения можно редактировать, но трафик обслуживаться не будет.</Alert>}
        {st?.lastError && <Alert>{st.lastError}</Alert>}
      </div>

      {d && st && sys && c && (
        <>
          <div className="grid grid-cols-2 gap-3 min-[640px]:grid-cols-3 min-[1100px]:grid-cols-6">
            {tiles.map((t, i) => <StatTile key={t.id} t={t} i={i} onOpen={() => nav(t.filter === 'all' ? '/clients' : `/clients?f=${t.filter}`)} />)}
          </div>

          <Card className="mt-3 p-[18px]">
            <div className="flex items-center justify-between gap-3">
              <h2 className="text-sm font-semibold">Состав базы</h2>
              <div className="flex flex-wrap justify-end gap-x-4 gap-y-1 text-xs text-dim">
                {mix.map((m) => (
                  <span key={m.label} className="inline-flex items-center gap-1.5"><i className="size-[7px] rounded-full" style={{ background: m.tone }} />{m.label} <b className="font-semibold text-ink tabular-nums">{m.n}</b></span>
                ))}
              </div>
            </div>
            <div className="mt-3.5 flex h-2 gap-[3px] overflow-hidden rounded-full bg-surface-2">
              {c.total > 0 && mix.filter((m) => m.n > 0).map((m, i) => (
                <i key={m.label} className="ap-grow-x block h-full rounded-[3px]" title={`${m.label}: ${m.n}`} style={{ flex: m.n, background: m.tone, animationDelay: `${i * 70}ms` }} />
              ))}
            </div>
          </Card>

          <div className="mt-3 grid gap-3 min-[1100px]:grid-cols-[minmax(0,1.9fr)_minmax(0,1fr)]">
            <Card className="p-[18px]">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h2 className="text-sm font-semibold">Трафик за 24 часа</h2>
                <div className="flex gap-3.5 text-xs text-dim">
                  <span className="flex items-center gap-1.5"><i className="size-2 rounded-[3px] bg-brand" />загрузка</span>
                  <span className="flex items-center gap-1.5"><i className="size-2 rounded-[3px] bg-brand/40" />отдача</span>
                </div>
              </div>
              <HistoryChart points={d.history} />
            </Card>

            <Card className="p-[18px]">
              <div className="flex items-center justify-between gap-2">
                <h2 className="text-sm font-semibold">Топ-5 клиентов</h2>
                <Segmented
                  small
                  label="Период"
                  value={period}
                  onChange={setPeriod}
                  items={[
                    { id: 'day', label: '1д', title: 'За последние сутки' },
                    { id: 'week', label: '7д', title: 'За последние 7 дней' },
                    { id: 'all', label: 'LT', title: 'За всё время' },
                  ]}
                />
              </div>
              {top.length === 0 ? (
                <p className="mt-4 text-sm text-faint">{period === 'all' ? 'Пока нет данных' : 'Пока нет данных за этот период'}</p>
              ) : (
                <ol className="mt-4 grid gap-3.5">
                  {top.map((u, i) => {
                    const online = d.onlineNames.includes(u.name)
                    return (
                      <li key={u.name} className="grid grid-cols-[22px_1fr_auto] items-center gap-x-2.5 gap-y-1">
                        <span className="text-center text-xs font-semibold text-faint">{i + 1}</span>
                        <span className="flex min-w-0 items-center gap-1.5 font-medium"><span className="truncate">{u.name}</span>{online && <i className="ap-pulse size-1.5 shrink-0 rounded-full bg-online [--c:var(--st-online)]" />}</span>
                        <span className="text-[13px] text-dim tabular-nums">{formatBytes(u.used)}</span>
                        <div className="col-[2/-1] h-1 overflow-hidden rounded-full bg-surface-2"><div className="ap-grow-x h-full rounded-full bg-ink/80" style={{ width: `${(u.used / top[0].used) * 100}%`, animationDelay: `${i * 80}ms` }} /></div>
                      </li>
                    )
                  })}
                </ol>
              )}
            </Card>
          </div>

          <div className="mt-3 grid gap-3 min-[900px]:grid-cols-3">
            <Card className="p-[18px]">
              <h2 className="text-sm font-semibold">Сервер</h2>
              {hasSys ? (
                <div className="mt-3.5 grid gap-3.5">
                  <Meter label={`Процессор (${sys.cpus} яд.)`} value={sys.cpuPercent} text={`${sys.cpuPercent.toFixed(0)}%`} />
                  <Meter label="Память" value={(sys.memUsed / sys.memTotal) * 100} text={`${formatBytes(sys.memUsed)} из ${formatBytes(sys.memTotal)}`} delay={80} />
                  <Meter label="Диск" value={sys.diskTotal ? (sys.diskUsed / sys.diskTotal) * 100 : 0} text={`${formatBytes(sys.diskUsed)} из ${formatBytes(sys.diskTotal)}`} delay={160} />
                </div>
              ) : (
                <p className="mt-3 text-sm text-faint">Показатели сервера доступны только на Linux</p>
              )}
            </Card>

            <Card className="p-[18px] min-[900px]:col-span-2">
              <div className="flex items-center justify-between">
                <h2 className="text-sm font-semibold">Сейчас онлайн</h2>
                <span className="inline-flex h-[22px] items-center gap-1.5 rounded-full bg-online/15 px-2.5 text-xs font-medium text-online"><i className="ap-pulse size-1.5 rounded-full bg-online [--c:var(--st-online)]" />{c.online}</span>
              </div>
              {d.onlineNames.length === 0 ? (
                <p className="mt-3.5 text-sm text-faint">Никого</p>
              ) : (
                <div className="mt-3.5 flex flex-wrap gap-2">
                  {d.onlineNames.map((n) => (
                    <span key={n} className="inline-flex h-8 items-center gap-2 rounded-full bg-surface-2 pl-1.5 pr-3 text-[12.5px] font-medium">
                      <span className="grid size-[22px] place-items-center rounded-full bg-surface text-[10px] font-semibold text-dim">{n.slice(0, 1).toUpperCase()}</span>
                      {n}
                    </span>
                  ))}
                </div>
              )}
            </Card>
          </div>
        </>
      )}

      <Card className="mt-3">
        <button
          type="button"
          aria-expanded={logOpen}
          onClick={() => setLogOpen((o) => !o)}
          className="flex w-full items-center justify-between px-[18px] py-3 text-left"
        >
          <h2 className="text-sm font-semibold">Журнал Xray</h2>
          <Icon name="down" className={`text-dim transition-transform duration-300 ease-[var(--ease)] ${logOpen ? 'rotate-180' : ''}`} />
        </button>
        {logOpen && (
          <div className="ap-enter px-[18px] pb-4">
            <pre className="max-h-72 overflow-auto font-mono text-xs leading-[1.7] text-dim">{logs.data?.lines?.length ? logs.data.lines.join('\n') : 'Пока пусто'}</pre>
            <Button variant="ghost" className="mt-2" onClick={() => void logs.reload()}><Icon name="refresh" className="size-4" />Обновить</Button>
          </div>
        )}
      </Card>
    </section>
  )
}
