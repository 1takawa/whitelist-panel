import { useEffect, useState, type FormEvent } from 'react'
import { api, apiUpload, ApiError } from '../api'
import { Tabs } from '../components/Tabs'
import TemplateField from '../components/TemplateField'
import { Alert, Button, Card, Field, Input, PageHeader, Select, Toggle } from '../components/ui'
import AppPreview, { type AppId } from '../components/AppPreview'
import { useLoad, useMediaQuery } from '../hooks'
import type { LinkPreview, PageSettings, ProfileMode, Settings, SubSettings, TemplateToken } from '../types'
import FormatsTab from './subscription/FormatsTab'
import HappTab from './subscription/HappTab'
import IncyTab from './subscription/IncyTab'
import AutoNodeTab from './subscription/AutoNodeTab'
import PageTab from './subscription/PageTab'

const TABS = [
  { id: 'general', label: 'Общие' },
  { id: 'information', label: 'Information' },
  { id: 'profile', label: 'Profile' },
  { id: 'preview', label: 'Ссылка' },
  { id: 'formats', label: 'Форматы' },
  { id: 'auto', label: 'Автоузел' },
  { id: 'page', label: 'Страница' },
  { id: 'happ', label: 'Happ' },
  { id: 'incy', label: 'Incy' },
]

// A saved document from before the page existed has no page block.
const NO_PAGE: PageSettings = { enable: false, html: '', css: '', js: '' }

const SAMPLE_ID = 'k3j9x0q2m7p5v8ab'

// The address clients will use for a path prefix, shown live while it is edited. A full URI replaces the host and
// the main path only; the other formats keep their own prefix on the same host.
function addressFor(s: Settings, path: string, useUri: boolean): string {
  if (useUri && s.subscription.uri) return s.subscription.uri.replace(/\/+$/, '') + '/' + SAMPLE_ID
  const host = s.subscription.domain || s.panelDomain
  if (!host) return ''
  const p = path || '/sub/'
  return `https://${host}${p.startsWith('/') ? p : '/' + p}${p.endsWith('/') ? '' : '/'}${SAMPLE_ID}`
}

export default function Subscription() {
  const settings = useLoad<Settings>('/api/settings')
  const tokens = useLoad<TemplateToken[]>('/api/settings/tokens')
  const [form, setForm] = useState<Settings | null>(null)
  const [tab, setTab] = useState('general')
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [imgBusy, setImgBusy] = useState(false)
  const [imgMsg, setImgMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const [previewApp, setPreviewApp] = useState<AppId>('happ')
  const wide = useMediaQuery('(min-width: 1180px)')

  // Opening the Happ or Incy tab switches the preview to that app.
  useEffect(() => { if (tab === 'happ' || tab === 'incy') setPreviewApp(tab) }, [tab])

  useEffect(() => { if (settings.data) setForm(settings.data) }, [settings.data])

  if (!form) {
    return (
      <section>
        <PageHeader title="Подписка" />
        {settings.error ? <Alert>{settings.error}</Alert> : <p className="text-sm text-dim">Загрузка…</p>}
      </section>
    )
  }

  const sub = form.subscription
  const set = (patch: Partial<SubSettings>) => setForm((f) => (f ? { ...f, subscription: { ...f.subscription, ...patch } } : f))
  const tk = tokens.data ?? []
  // The side panel shows what the tab is about: the link card for «Ссылка», the app for the app tabs, nothing for the formats.
  const side = tab !== 'formats' && tab !== 'page'
  const appTab = tab === 'happ' || tab === 'incy'
  const shownApp: AppId = tab === 'happ' || tab === 'incy' ? tab : previewApp
  const appTitle = tab === 'happ' ? 'Предпросмотр в Happ' : tab === 'incy' ? 'Предпросмотр в INCY' : 'Предпросмотр в приложении'
  const linkCard = <LinkCard preview={sub.preview} host={sub.domain || form.panelDomain || 'panel.example.com'} url={addressFor(form, sub.path, true)} />
  const setPreview = (p: Partial<LinkPreview>) => set({ preview: { ...sub.preview, ...p } })

  // The picture is saved by its own request; only its description is taken from the answer, so unsaved edits stay.
  const takeImage = (s: Settings) => setPreview({ image: s.subscription.preview.image })
  async function uploadImage(file?: File) {
    if (!file) return
    setImgBusy(true)
    setImgMsg(null)
    try {
      takeImage(await apiUpload<Settings>('/api/settings/preview-image', file))
      setImgMsg({ ok: true, text: 'Картинка загружена.' })
    } catch (err) {
      setImgMsg({ ok: false, text: err instanceof ApiError ? err.message : String(err) })
    } finally {
      setImgBusy(false)
    }
  }
  async function removeImage() {
    setImgBusy(true)
    setImgMsg(null)
    try {
      takeImage(await api<Settings>('/api/settings/preview-image', { method: 'DELETE' }))
    } catch (err) {
      setImgMsg({ ok: false, text: err instanceof ApiError ? err.message : String(err) })
    } finally {
      setImgBusy(false)
    }
  }
  const url = addressFor(form, sub.path, true)

  async function save(e: FormEvent) {
    e.preventDefault()
    if (!form) return
    setBusy(true)
    setMsg(null)
    try {
      setForm(await api<Settings>('/api/settings', { method: 'PUT', body: form }))
      setMsg({ ok: true, text: 'Сохранено.' })
    } catch (err) {
      setMsg({ ok: false, text: err instanceof ApiError ? err.message : 'Не удалось сохранить' })
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="max-w-6xl">
      <PageHeader title="Подписка" />
      <div className={`grid items-start gap-6 ${side ? 'min-[1180px]:grid-cols-[minmax(0,1fr)_400px]' : ''}`}>
      <form onSubmit={save} className={`min-w-0 ${side ? 'max-w-3xl' : 'max-w-5xl'}`}>
        <Tabs tabs={TABS} active={tab} onChange={setTab}>
          {tab === 'general' && (
            <Card className="grid gap-4 p-5">
              <div>
                <Toggle label="Подписка включена" checked={sub.enable} onChange={(v) => set({ enable: v })} />
                <p className="mt-1 text-xs text-dim">Если выключить, все адреса подписки перестанут отвечать (404).</p>
              </div>
              <Field label="Путь подписки" hint="Префикс адреса. Можно сделать менее очевидным, например /s/k3j9x/. Не может начинаться с /api, /assets, /login и других служебных путей.">
                <Input value={sub.path} onChange={(e) => set({ path: e.target.value })} placeholder="/sub/" />
              </Field>
              <Field label="Домен подписки" hint="Если пусто — домен панели. Нужен, когда подписку отдают через другой домен, например за CDN.">
                <Input value={sub.domain} onChange={(e) => set({ domain: e.target.value })} placeholder={form.panelDomain || 'sub.example.com'} />
              </Field>
              <Field label="Полный адрес (URI)" hint="Переопределяет домен и путь целиком, например https://edge.example.com/prefix/. Адрес только подставляется в ссылки: маршрутизацию на него настраиваете вы.">
                <Input value={sub.uri} onChange={(e) => set({ uri: e.target.value })} placeholder="https://" />
              </Field>
              <div className="rounded-lg bg-surface-2 p-3 text-sm">
                <div className="text-xs text-dim">Так будет выглядеть адрес подписки клиента</div>
                <code className="mt-1 block break-all text-xs">{url || 'Задайте домен панели в «Настройках»'}</code>
              </div>
              <div>
                <Toggle label="Скрывать настройки в приложениях" checked={sub.hideSettings} onChange={(v) => set({ hideSettings: v })} />
                <p className="mt-1 text-xs text-dim">Отправляет заголовок Hide-Settings: приложения, которые его понимают (например, Happ), прячут экран настроек.</p>
              </div>
            </Card>
          )}

          {tab === 'information' && (
            <Card className="grid gap-5 p-5">
              <div>
                <Toggle label="Кодировать ответ в base64" checked={sub.encode} onChange={(v) => set({ encode: v })} />
                <p className="mt-1 text-xs text-dim">Рекомендуется: так подписку понимают все приложения. Если выключить, ссылки отдаются обычным текстом, по одной в строке.</p>
              </div>
              <TemplateField
                label="Название ссылки" kind="remark" tokens={tk} timeZone={sub.timeZone}
                value={sub.remarkTemplate} onChange={(v) => set({ remarkTemplate: v })}
                hint="Так называется каждое подключение в приложении. Части, разделённые «|», пропадают, если для клиента нет значения (например, лимит трафика не задан)."
              />
              <div>
                <Toggle label="Информационный узел" checked={sub.infoNode} onChange={(v) => set({ infoNode: v })} />
                <p className="mt-1 text-xs text-dim">
                  Добавляет в список приложения строку без подключения, в названии которой — остаток трафика и срок. Когда срок или трафик закончились,
                  она остаётся единственной и показывает причину.
                </p>
              </div>
              <div className="grid gap-4 sm:grid-cols-2">
                <div>
                  <Toggle label="Последний день срока включительно" checked={sub.calendarExpire} onChange={(v) => set({ calendarExpire: v })} />
                  <p className="mt-1 text-xs text-dim">Подписка действует до конца даты окончания, а не до точной минуты. Это учитывает и Xray.</p>
                </div>
                <Field label="Часовой пояс" hint="Для дат и конца дня. Пусто — время сервера.">
                  <Input value={sub.timeZone} onChange={(e) => set({ timeZone: e.target.value })} placeholder="Europe/Moscow" />
                </Field>
              </div>
              <TemplateField
                label="Название при истёкшем сроке" kind="remark" tokens={tk} timeZone={sub.timeZone}
                value={sub.expiredTemplate} onChange={(v) => set({ expiredTemplate: v })}
              />
              <TemplateField
                label="Название при исчерпанном трафике" kind="remark" tokens={tk} timeZone={sub.timeZone}
                value={sub.depletedTemplate} onChange={(v) => set({ depletedTemplate: v })}
              />
              <Field label="Интервал обновления, часов" hint="Как часто приложения перезапрашивают подписку (заголовок Profile-Update-Interval).">
                <Input type="number" min={1} max={8760} value={sub.updateHours} onChange={(e) => set({ updateHours: Number(e.target.value) })} />
              </Field>
            </Card>
          )}

          {tab === 'profile' && (
            <Card className="grid gap-5 p-5">
              <TemplateField
                label="Название профиля" kind="meta" tokens={tk} maxLength={100}
                value={sub.title} onChange={(v) => set({ title: v })}
                hint="Приложения показывают его как название подписки. Можно подставить имя клиента."
              />
              <TemplateField
                label="Ссылка на поддержку" kind="url" tokens={tk} placeholder="https://t.me/…"
                value={sub.supportUrl} onChange={(v) => set({ supportUrl: v })}
              />
              <div className="grid gap-3">
                <Field label="Страница профиля" hint="Ссылка «Профиль» в приложении.">
                  <Select value={sub.profileMode} onChange={(e) => set({ profileMode: e.target.value as ProfileMode })}>
                    <option value="none">Не показывать</option>
                    <option value="builtin">Страница подписки панели (адрес подписки клиента)</option>
                    <option value="custom">Своя ссылка</option>
                  </Select>
                </Field>
                {sub.profileMode === 'custom' && (
                  <TemplateField
                    label="Своя ссылка" kind="url" tokens={tk} placeholder="https://example.com/cabinet/{{SUB_ID}}"
                    value={sub.profileUrl} onChange={(v) => set({ profileUrl: v })}
                  />
                )}
              </div>
              <TemplateField
                label="Объявление" kind="meta" tokens={tk} maxLength={200}
                value={sub.announce} onChange={(v) => set({ announce: v })}
                hint="Показывается в приложении и на странице подписки (до 200 символов)."
              />
            </Card>
          )}

          {tab === 'preview' && (
            <Card className="grid gap-5 p-5">
              <p className="text-sm text-dim">
                Так выглядит ссылка на подписку, когда её отправляют в Telegram, WhatsApp и другие мессенджеры: название сайта, заголовок, описание и картинка.
                Мессенджер запоминает карточку, поэтому после изменения у уже отправленных ссылок она может обновиться не сразу.
              </p>
              <Field label="Вид карточки">
                <Select value={sub.preview.mode} onChange={(e) => setPreview({ mode: e.target.value as LinkPreview['mode'] })}>
                  <option value="default">По умолчанию (адрес сайта, «Whitelist Panel» и описание панели)</option>
                  <option value="custom">Свой</option>
                </Select>
              </Field>
              {sub.preview.mode === 'custom' && (
                <>
                  <TemplateField
                    label="Название сайта" kind="meta" tokens={tk} maxLength={60}
                    value={sub.preview.siteName} onChange={(v) => setPreview({ siteName: v })}
                    hint="Маленькая строка над заголовком. Пусто — мессенджер покажет адрес сайта."
                  />
                  <TemplateField
                    label="Заголовок" kind="meta" tokens={tk} maxLength={100}
                    value={sub.preview.title} onChange={(v) => setPreview({ title: v })}
                    hint="Крупная строка. Пусто — «Whitelist Panel». Можно подставить имя клиента."
                  />
                  <TemplateField
                    label="Описание" kind="meta" tokens={tk} maxLength={300}
                    value={sub.preview.description} onChange={(v) => setPreview({ description: v })}
                    hint="Пусто — описания в карточке не будет."
                  />
                  <div className="grid gap-2 rounded-lg border border-line p-3">
                    <div className="text-sm font-medium">Картинка в карточке <span className="ml-1 rounded bg-surface-2 px-1.5 py-0.5 text-xs font-normal text-dim">тестовая функция</span></div>
                    <p className="text-xs text-dim">
                      Загрузите файл в панель (PNG, JPEG, GIF или WebP, до 2 МБ, лучше горизонтальный, около 1200×630): панель сама отдаст его мессенджеру
                      и покажет в карточке большой картинкой рядом со ссылкой. Загруженная картинка сразу в силе, «Сохранить» для неё не нужно.
                    </p>
                    {sub.preview.image.type && <img src={`/api/sub-preview?v=${sub.preview.image.sum}`} alt="" className="max-h-40 w-fit rounded-md border border-line" />}
                    <div className="flex flex-wrap items-center gap-2">
                      <label className="inline-block">
                        <input type="file" accept="image/png,image/jpeg,image/gif,image/webp" className="hidden" disabled={imgBusy} onChange={(e) => { void uploadImage(e.target.files?.[0]); e.target.value = '' }} />
                        <span className={`inline-block cursor-pointer rounded-lg border border-line bg-surface px-3 py-1.5 text-sm font-medium hover:bg-surface-2 ${imgBusy ? 'opacity-50' : ''}`}>{imgBusy ? 'Загружаем…' : sub.preview.image.type ? 'Заменить картинку' : 'Загрузить картинку'}</span>
                      </label>
                      {sub.preview.image.type && <Button variant="ghost" disabled={imgBusy} onClick={() => void removeImage()}>Удалить картинку</Button>}
                      {sub.preview.image.width > 0 && <span className="text-xs text-dim">{sub.preview.image.width}×{sub.preview.image.height}</span>}
                    </div>
                    {imgMsg && <Alert tone={imgMsg.ok ? 'ok' : 'bad'}>{imgMsg.text}</Alert>}
                    <Field label="Или адрес картинки" hint="Используется, если картинка не загружена: https://…">
                      <Input value={sub.preview.imageUrl} onChange={(e) => setPreview({ imageUrl: e.target.value })} placeholder="https://example.com/logo.png" spellCheck={false} />
                    </Field>
                  </div>
                </>
              )}
            </Card>
          )}

          {tab === 'formats' && <FormatsTab value={sub.formats} onChange={(p) => set({ formats: { ...sub.formats, ...p } })} example={(path) => addressFor(form, path, false)} />}
          {tab === 'auto' && <AutoNodeTab value={sub.autoNode} onChange={(p) => set({ autoNode: { ...sub.autoNode, ...p } })} jsonEnabled={sub.formats.jsonEnable} clashEnabled={sub.formats.clashEnable} />}
          {tab === 'page' && <PageTab value={{ ...NO_PAGE, ...sub.page }} onChange={(p) => set({ page: { ...NO_PAGE, ...sub.page, ...p } })} />}
          {tab === 'happ' && <HappTab value={sub.happ} onChange={(p) => set({ happ: { ...sub.happ, ...p } })} />}
          {tab === 'incy' && <IncyTab value={sub.incy} onChange={(p) => set({ incy: { ...sub.incy, ...p } })} />}
        </Tabs>

        <div className="mt-5 grid gap-3">
          {msg && <Alert tone={msg.ok ? 'ok' : 'bad'}>{msg.text}</Alert>}
          <div><Button type="submit" variant="primary" disabled={busy}>{busy ? 'Сохраняем…' : 'Сохранить'}</Button></div>
        </div>
      </form>

      {side && (
      <aside className="min-[1180px]:sticky min-[1180px]:top-4" aria-label={tab === 'preview' ? 'Предпросмотр ссылки' : 'Предпросмотр в приложении'}>
        {tab === 'preview' ? (
          <Card className="p-4">
            <h2 className="mb-3 text-sm font-semibold">Предпросмотр в мессенджере</h2>
            {linkCard}
          </Card>
        ) : wide ? (
          <Card className="p-4">
            <h2 className="mb-3 text-sm font-semibold">{appTitle}</h2>
            <AppPreview sub={sub} app={shownApp} onApp={setPreviewApp} locked={appTab} />
          </Card>
        ) : (
          <details className="rounded-xl border border-line bg-surface p-4">
            <summary className="cursor-pointer select-none text-sm font-semibold">{appTitle}</summary>
            <div className="mt-3"><AppPreview sub={sub} app={shownApp} onApp={setPreviewApp} locked={appTab} /></div>
          </details>
        )}
      </aside>
      )}
      </div>
    </section>
  )
}

// A rough picture of the card a messenger draws under the link (placeholders show a sample client).
function LinkCard({ preview, host, url }: { preview: LinkPreview; host: string; url: string }) {
  const sample = (v: string) => v.replace(/\{\{EMAIL\}\}/g, 'alice').replace(/\{\{SHORT_ID\}\}/g, SAMPLE_ID.slice(0, 8)).replace(/\{\{SUB_ID\}\}/g, SAMPLE_ID).replace(/\{\{ID\}\}/g, SAMPLE_ID)
  const custom = preview.mode === 'custom'
  const site = custom ? sample(preview.siteName) || host : host
  const title = custom ? sample(preview.title) || 'Whitelist Panel' : 'Whitelist Panel'
  const desc = custom ? sample(preview.description) : 'Whitelist Panel — управление Xray: VLESS и Trojan'
  return (
    <div>
      <div className="max-w-md break-all text-sm text-accent">{url || 'https://' + host + '/sub/…'}</div>
      <div className="mt-2 max-w-md overflow-hidden rounded-lg border-l-4 border-accent bg-surface-2 p-3">
        <div className="text-sm font-semibold text-danger">{site}</div>
        <div className="text-sm font-semibold">{title}</div>
        {desc && <div className="text-sm">{desc}</div>}
        {custom && (preview.image.type || preview.imageUrl) && (
          <img src={preview.image.type ? `/api/sub-preview?v=${preview.image.sum}` : preview.imageUrl} alt="" className="mt-2 max-h-48 rounded-md object-cover" onError={(e) => { e.currentTarget.style.display = 'none' }} />
        )}
      </div>
    </div>
  )
}
