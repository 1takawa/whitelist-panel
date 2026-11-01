import { useEffect, useState, type ReactNode } from 'react'
import { useParams } from 'react-router'
import Qr from '../components/Qr'
import { Icon } from '../components/Icon'
import { Alert, Badge, Card, CopyButton, CopyIcon, Hint, IconButton, StatusChip, type Tone } from '../components/ui'
import { useLoad } from '../hooks'
import { copyText, formatBytes, formatDate } from '../lib'
import { useTheme } from '../theme'
import type { PublicSub } from '../types'

const BRAND = 'whitelist'

const statusView: Record<PublicSub['status'], { label: string; tone: Tone }> = {
  active: { label: 'Активна', tone: 'ok' },
  disabled: { label: 'Отключена', tone: 'neutral' },
  expired: { label: 'Срок истёк', tone: 'warn' },
  exhausted: { label: 'Трафик исчерпан', tone: 'warn' },
}

function daysLeft(iso: string): number {
  return Math.ceil((new Date(iso).getTime() - Date.now()) / 86400000)
}

function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10
  const m100 = n % 100
  if (m10 === 1 && m100 !== 11) return one
  if (m10 >= 2 && m10 <= 4 && (m100 < 10 || m100 >= 20)) return few
  return many
}

// Apps that take a subscription. `open` builds the link that adds it in one tap; an app without one (or one the panel
// cannot vouch for) only gets the copy button.
const apps: { name: string; platforms: string; open?: (url: string) => string }[] = [
  { name: 'Happ', platforms: 'iOS · Android · macOS · Windows', open: (u) => `happ://add/${u}` },
  { name: 'INCY', platforms: 'iOS · Android · macOS' },
  { name: 'Hiddify', platforms: 'Android · iOS · Windows · macOS · Linux', open: (u) => `hiddify://import/${u}` },
  { name: 'v2rayTun', platforms: 'iOS · Android · Windows · macOS', open: (u) => `v2raytun://import/${u}` },
]

const addBtn = 'inline-flex h-8 items-center rounded-lg bg-accent px-3.5 text-sm font-medium text-accent-ink transition-[opacity,transform] hover:opacity-85 active:scale-[0.97]'

// For an app without a one-tap link the button copies the subscription, and says so for a moment.
function CopyPill({ text, className }: { text: string; className: string }) {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className={className}
      onClick={async () => {
        if (await copyText(text)) {
          setDone(true)
          setTimeout(() => setDone(false), 1500)
        }
      }}
    >
      {done ? 'Скопировано' : 'Копировать'}
    </button>
  )
}

function Tile({ label, children, sub }: { label: string; children: ReactNode; sub?: ReactNode }) {
  return (
    <div className="rounded-2xl bg-surface-2/60 p-4">
      <div className="text-xs text-faint">{label}</div>
      <div className="mt-1.5 text-[19px] font-semibold tracking-tight tabular-nums">{children}</div>
      {sub}
    </div>
  )
}

// Public page for a subscriber: reached by a browser at /sub/<id>; apps get the machine-readable form from the same address.
export default function SubPage() {
  // Either /sub/:subId or, for a custom prefix, the last segment of whatever path was opened.
  const params = useParams()
  const subId = params.subId ?? (params['*'] ?? '').split('/').filter(Boolean).pop() ?? ''
  const { data, error, loading } = useLoad<PublicSub>(`/api/sub/${encodeURIComponent(subId)}`)
  const [qr, setQr] = useState<string | null>(null)
  const [open, setOpen] = useState(false)
  const { theme, toggle } = useTheme()

  const brand = data?.title?.trim() || BRAND
  useEffect(() => {
    document.title = brand
  }, [brand])

  if (loading) return <main className="grid min-h-full place-items-center text-sm text-dim">Загрузка…</main>
  if (error || !data) {
    return (
      <main className="grid min-h-full place-items-center px-4">
        <Card className="ap-enter max-w-sm p-7 text-center">
          <h1 className="text-lg font-semibold">Подписка не найдена</h1>
          <p className="mt-2 text-sm text-dim">Проверьте ссылку или обратитесь к тому, кто её выдал.</p>
        </Card>
      </main>
    )
  }

  const used = data.up + data.down
  const st = statusView[data.status]
  const pct = data.total > 0 ? Math.min(100, (used / data.total) * 100) : 0
  const left = data.expiryAt ? daysLeft(data.expiryAt) : null

  return (
    <div className="relative min-h-full overflow-x-clip">
      <div aria-hidden="true" className="pointer-events-none absolute -top-60 left-1/2 size-[720px] -translate-x-1/2 rounded-full bg-[radial-gradient(closest-side,var(--brand-soft),transparent)]" />
      <main className="ap-enter relative mx-auto w-full max-w-3xl px-4 pb-14 pt-6 md:pt-10">
        <header className="mb-6 flex items-center justify-between">
          <div className="flex items-center gap-2.5 text-[17px] font-semibold tracking-tight">
            <span className="grid size-8 place-items-center rounded-[10px] bg-accent text-sm font-bold uppercase text-accent-ink">{brand.slice(0, 1)}</span>
            {brand}
          </div>
          <IconButton icon={theme === 'dark' ? 'sun' : 'moon'} label="Сменить тему" onClick={toggle} />
        </header>

        {data.announce && <div className="mb-4"><Alert tone="warn">{data.announce}</Alert></div>}

        <Card className="overflow-hidden">
          <div className="grid gap-6 p-5 md:grid-cols-[1fr_auto] md:p-7">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-3">
                <h1 className="truncate text-[28px] font-bold leading-tight tracking-[-0.03em]">{data.name}</h1>
                <StatusChip tone={st.tone}>{st.label}</StatusChip>
              </div>

              <div className="mt-5 grid gap-3 sm:grid-cols-2">
                <Tile
                  label="Трафик"
                  sub={
                    data.total > 0 && (
                      <div className="mt-2.5">
                        <div className="h-1.5 overflow-hidden rounded-full bg-line" role="progressbar" aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100} aria-label="Использованный трафик">
                          <div className={`ap-grow-x h-full rounded-full ${pct >= 90 ? 'bg-blocked' : pct >= 70 ? 'bg-expired' : 'bg-brand'}`} style={{ width: `${pct}%` }} />
                        </div>
                        <div className="mt-1.5 text-xs text-faint">Осталось {formatBytes(Math.max(0, data.total - used))}</div>
                      </div>
                    )
                  }
                >
                  {formatBytes(used)}
                  <span className="text-sm font-normal text-faint"> / {data.total > 0 ? formatBytes(data.total) : '∞'}</span>
                </Tile>
                <Tile
                  label="Действует до"
                  sub={left !== null && <div className="mt-2.5 text-xs text-faint">{left > 0 ? `Осталось ${left} ${plural(left, 'день', 'дня', 'дней')}` : 'Срок истёк'}</div>}
                >
                  {data.expiryAt ? formatDate(data.expiryAt) : 'Бессрочно'}
                </Tile>
              </div>
            </div>

            {data.url && (
              <div className="flex flex-col items-center gap-2 md:items-end">
                <Qr text={data.url} size={176} />
                <span className="text-xs text-faint">QR подписки</span>
              </div>
            )}
          </div>

          {data.url && (
            <div className="flex flex-wrap items-center gap-2 border-t border-line bg-surface-2/40 px-5 py-4 md:px-7">
              <input
                readOnly
                value={data.url}
                onFocus={(e) => e.currentTarget.select()}
                aria-label="Ссылка подписки"
                className="h-10 min-w-0 flex-1 basis-60 rounded-[10px] border border-line-strong bg-surface px-3 font-mono text-xs focus:border-brand focus:outline-none"
              />
              <CopyButton text={data.url} label="Копировать ссылку" variant="primary" className="h-10 max-[560px]:w-full" />
            </div>
          )}
        </Card>

        {data.url && (
          <section className="mt-7">
            <h2 className="mb-3 flex items-center gap-1.5 text-sm font-semibold">
              Добавить в приложение
              <Hint text="Кнопка откроет приложение и добавит подписку. Если оно не открылось, скопируйте ссылку и вставьте её в приложение вручную как подписку." />
            </h2>
            <Card className="divide-y divide-line overflow-hidden">
              {apps.map((a) => (
                <div key={a.name} className="flex h-12 items-center justify-between gap-3 pl-4 pr-2.5">
                  <span className="font-medium" title={a.platforms}>{a.name}</span>
                  {a.open ? (
                    <a href={a.open(data.url)} className={addBtn}>Добавить</a>
                  ) : (
                    <CopyPill text={data.url} className={addBtn} />
                  )}
                </div>
              ))}
            </Card>
          </section>
        )}

        <section className="mt-7">
          <button
            type="button"
            aria-expanded={open}
            onClick={() => setOpen((o) => !o)}
            className="flex h-11 w-full items-center justify-between rounded-2xl border border-line bg-surface px-4 text-left text-sm font-semibold shadow-card hover:border-line-strong"
          >
            <span>Ноды <span className="ml-1 font-medium text-faint tabular-nums">{data.links.length}</span></span>
            <Icon name="down" className={`text-dim transition-transform duration-300 ease-[var(--ease)] ${open ? 'rotate-180' : ''}`} />
          </button>
          {open && (
            <ul className="ap-enter mt-2 grid gap-2">
              {data.links.length === 0 && <li className="px-1 py-3 text-sm text-faint">Пока нет доступных подключений</li>}
              {data.links.map((l, i) => (
                <li key={i} className="rounded-2xl border border-line bg-surface px-4 py-3">
                  <div className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate font-medium">{l.remark}</span>
                    <Badge>{l.protocol.toUpperCase()} · {l.network} · {l.security}</Badge>
                    <CopyIcon text={l.link} label="Копировать ссылку ноды" />
                    <IconButton icon="qr" label={qr === String(i) ? 'Скрыть QR' : 'Показать QR'} onClick={() => setQr(qr === String(i) ? null : String(i))} className={qr === String(i) ? 'bg-surface-2 text-ink' : ''} />
                  </div>
                  {qr === String(i) && <div className="ap-enter mt-3 flex justify-center"><Qr text={l.link} /></div>}
                </li>
              ))}
            </ul>
          )}
        </section>

        {(data.supportUrl || data.profileUrl) && (
          <footer className="mt-8 flex flex-wrap justify-center gap-5 text-sm">
            {data.supportUrl && <a className="text-dim underline-offset-4 hover:text-ink hover:underline" href={data.supportUrl} target="_blank" rel="noopener noreferrer">Поддержка</a>}
            {data.profileUrl && <a className="text-dim underline-offset-4 hover:text-ink hover:underline" href={data.profileUrl} target="_blank" rel="noopener noreferrer">Сайт</a>}
          </footer>
        )}
      </main>
    </div>
  )
}
