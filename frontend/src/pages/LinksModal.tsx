import { useState } from 'react'
import Qr from '../components/Qr'
import { Icon } from '../components/Icon'
import { Alert, Badge, CopyButton, CopyIcon, IconButton, Modal } from '../components/ui'
import { useLoad } from '../hooks'
import type { Client, ClientLinks } from '../types'

export default function LinksModal({ client, onClose }: { client: Client; onClose: () => void }) {
  const { data, error, loading } = useLoad<ClientLinks>(`/api/clients/${client.id}/links`)
  const [qr, setQr] = useState<string | null>(null)
  const [open, setOpen] = useState(false) // the separate links are tucked away: the subscription is what people use
  const links = data?.links ?? []
  const subUrl = data?.subscriptionUrl ?? ''
  const toggleQr = (id: string) => setQr((cur) => (cur === id ? null : id))

  return (
    <Modal title={`Ссылки: ${client.email}`} onClose={onClose} wide>
      {error && <Alert>{error}</Alert>}

      {data && (
        <section className="rounded-2xl border border-line-strong bg-surface-2/50 p-4">
          <div className="mb-2.5 text-[13px] font-medium text-dim">Подписка</div>
          {subUrl ? (
            <>
              <div className="flex items-center gap-2">
                <input
                  readOnly
                  value={subUrl}
                  onFocus={(e) => e.currentTarget.select()}
                  aria-label="Ссылка подписки"
                  className="h-10 min-w-0 flex-1 rounded-[10px] border border-line-strong bg-surface px-3 font-mono text-xs focus:border-brand focus:outline-none"
                />
                <CopyButton text={subUrl} />
                <IconButton icon="qr" label={qr === 'sub' ? 'Скрыть QR' : 'Показать QR'} onClick={() => toggleQr('sub')} className={qr === 'sub' ? 'bg-surface-2 text-ink' : ''} />
              </div>
              {qr === 'sub' && <div className="ap-enter mt-4 flex justify-center"><Qr text={subUrl} /></div>}
            </>
          ) : (
            <Alert tone="warn">Задайте домен панели в «Настройках», чтобы получить ссылку подписки.</Alert>
          )}
        </section>
      )}

      {!loading && !error && links.length === 0 && <p className="mt-4 text-sm text-faint">Клиент не привязан ни к одному подключению</p>}

      {links.length > 0 && (
        <section className="mt-3">
          <button
            type="button"
            aria-expanded={open}
            onClick={() => setOpen((o) => !o)}
            className="flex h-10 w-full items-center justify-between rounded-xl px-2 text-left text-[13px] font-medium text-dim hover:bg-surface-2 hover:text-ink"
          >
            <span>Отдельные ссылки <span className="ml-1 text-faint tabular-nums">{links.length}</span></span>
            <Icon name="down" className={`transition-transform duration-300 ease-[var(--ease)] ${open ? 'rotate-180' : ''}`} />
          </button>
          {open && (
            <ul className="ap-enter mt-1 grid gap-1.5">
              {links.map((l) => (
                <li key={l.inboundId} className="rounded-xl border border-line px-3.5 py-2.5">
                  <div className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate font-medium">{l.remark}</span>
                    <Badge>{l.protocol.toUpperCase()} · {l.network} · {l.security}</Badge>
                    {!l.enabled && <Badge tone="warn">выключено</Badge>}
                    {!l.error && (
                      <>
                        <CopyIcon text={l.link} />
                        <IconButton icon="qr" label={qr === l.inboundId ? 'Скрыть QR' : 'Показать QR'} onClick={() => toggleQr(l.inboundId)} className={qr === l.inboundId ? 'bg-surface-2 text-ink' : ''} />
                      </>
                    )}
                  </div>
                  {l.error && <div className="mt-2"><Alert tone="warn">{l.error}</Alert></div>}
                  {qr === l.inboundId && !l.error && <div className="ap-enter mt-3 flex justify-center"><Qr text={l.link} /></div>}
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
    </Modal>
  )
}
