import { useState } from 'react'
import HappRoutingEditor from '../../components/HappRoutingEditor'
import { SectionTabs } from '../../components/SectionTabs'
import { Card, Field, Input, Select, Textarea, Toggle } from '../../components/ui'
import type { HappBanner, HappBanners, HappSettings } from '../../types'

type BannerKey = 'common' | keyof HappBanners

const bannerTabs: { id: BannerKey; label: string; hint: string }[] = [
  { id: 'common', label: 'Общий', hint: 'Показывается всем, у кого нет своего баннера для их статуса.' },
  { id: 'active', label: 'Активна', hint: 'Подписка действует и трафик не исчерпан.' },
  { id: 'trial', label: 'Тестовая', hint: 'Клиенты с галочкой «Тестовая подписка» (пока подписка действует).' },
  { id: 'expired', label: 'Срок истёк', hint: 'Срок подписки закончился.' },
  { id: 'depleted', label: 'Трафик исчерпан', hint: 'Лимит трафика использован.' },
]

// One banner: text with line breaks, colour and an optional button. The same form serves the common banner (the flat
// subInfo* settings) and the per-status ones.
function BannerEditor({ h, onChange }: { h: HappSettings; onChange: (patch: Partial<HappSettings>) => void }) {
  const [tab, setTab] = useState<BannerKey>('common')
  const banners = h.banners ?? emptyBanners
  const value: HappBanner =
    tab === 'common'
      ? { color: h.subInfoColor, text: h.subInfoText, buttonText: h.subInfoButtonText, buttonLink: h.subInfoButtonLink }
      : banners[tab]
  const set = (patch: Partial<HappBanner>) => {
    const next = { ...value, ...patch }
    if (tab === 'common') onChange({ subInfoColor: next.color, subInfoText: next.text, subInfoButtonText: next.buttonText, subInfoButtonLink: next.buttonLink })
    else onChange({ banners: { ...banners, [tab]: next } })
  }
  const filled = (id: BannerKey) => (id === 'common' ? h.subInfoText : banners[id].text).trim() !== ''
  const current = bannerTabs.find((t) => t.id === tab)!
  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap gap-1 rounded-lg border border-line p-0.5" role="tablist" aria-label="Баннер для статуса">
        {bannerTabs.map((t) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            aria-selected={tab === t.id}
            onClick={() => setTab(t.id)}
            className={`rounded-md px-2.5 py-1 text-xs font-medium ${tab === t.id ? 'bg-accent text-accent-ink' : 'text-dim hover:bg-surface-2'}`}
          >
            {t.label}
            {t.id !== 'common' && filled(t.id) && <span aria-hidden className="ml-1">•</span>}
          </button>
        ))}
      </div>
      <p className="text-xs text-dim">
        {current.hint} {tab !== 'common' && 'Если текст пуст, используется общий баннер.'} Баннер видит только Happ; это же отображается в предпросмотре справа
        (переключатель состояния над макетом).
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="sm:col-span-2">
          <Field label="Текст" hint={`Enter — новая строка (до 10 строк). Можно подставлять {{EMAIL}}, {{DAYS_LEFT}}, {{EXPIRE_DATE}}, {{TRAFFIC_LEFT}}, {{TRAFFIC_USED}}. Пусто — баннера нет. ${[...value.text].length}/200`}>
            <Textarea rows={3} maxLength={200} value={value.text} onChange={(e) => set({ text: e.target.value })} />
          </Field>
        </div>
        <Field label="Цвет">
          <Select value={value.color || 'blue'} onChange={(e) => set({ color: e.target.value })}>
            <option value="blue">Синий</option>
            <option value="green">Зелёный</option>
            <option value="red">Красный</option>
          </Select>
        </Field>
        <Field label="Текст кнопки">
          <Input value={value.buttonText} maxLength={25} onChange={(e) => set({ buttonText: e.target.value })} />
        </Field>
        <Field label="Ссылка кнопки" hint="https:// или tg://">
          <Input value={value.buttonLink} placeholder="https://" onChange={(e) => set({ buttonLink: e.target.value })} />
        </Field>
      </div>
      <p className="text-xs text-dim">
        Несколько строк передаются приложению в виде base64. Если ваша версия Happ покажет такой текст в одну строку или как «base64:…», оставьте баннер из одной строки.
      </p>
    </div>
  )
}

const emptyBanner: HappBanner = { color: 'blue', text: '', buttonText: '', buttonLink: '' }
const emptyBanners: HappBanners = { active: emptyBanner, trial: emptyBanner, expired: emptyBanner, depleted: emptyBanner }

const SECTIONS = [
  { id: 'main', label: 'Основное' },
  { id: 'banner', label: 'Баннер' },
  { id: 'behavior', label: 'Поведение' },
  { id: 'routing', label: 'Маршрутизация' },
]

export default function HappTab({ value: h, onChange }: { value: HappSettings; onChange: (patch: Partial<HappSettings>) => void }) {
  const text = (key: keyof HappSettings, label: string, hint?: string, placeholder?: string) => (
    <Field label={label} hint={hint}>
      <Input value={h[key] as string} placeholder={placeholder} onChange={(e) => onChange({ [key]: e.target.value } as Partial<HappSettings>)} />
    </Field>
  )
  const [section, setSection] = useState('main')
  return (
    <SectionTabs sections={SECTIONS} active={section} onChange={setSection}>
      {section === 'main' && (
      <Card className="grid gap-4 p-5">
        <div>
          <Toggle label="Управлять приложением Happ" checked={h.autoDetect} onChange={(v) => onChange({ autoDetect: v })} />
          <p className="mt-1 text-xs text-dim">
            Заголовки ниже отправляются только приложению Happ (его определяют по User-Agent). Остальные приложения их не видят. Пока переключатель выключен,
            панель ничего не меняет в настройках Happ у пользователей.
          </p>
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          {text('providerId', 'ID провайдера', 'Заголовок ProviderID')}
          {text('newUrl', 'Новый адрес подписки', 'Приложение перейдёт на этот адрес (New-Url)', 'https://')}
          {text('fallbackUrl', 'Запасной адрес', 'Если основной недоступен (Fallback-Url)', 'https://')}
        </div>
      </Card>
      )}

      {section === 'banner' && (
      <Card className="grid gap-4 p-5">
        <h2 className="text-sm font-semibold">Информационный баннер</h2>
        <BannerEditor h={h} onChange={onChange} />
        <div className="grid gap-3 sm:grid-cols-2">
          <div>
            <Toggle label="Предупреждать об окончании срока" checked={h.subExpire} onChange={(v) => onChange({ subExpire: v })} />
            {h.subExpire && <div className="mt-2">{text('subExpireButtonLink', 'Ссылка кнопки продления', undefined, 'https://')}</div>}
          </div>
          <Toggle label="Уведомление об окончании подписки" checked={h.notificationExpire} onChange={(v) => onChange({ notificationExpire: v })} />
        </div>
      </Card>
      )}

      {section === 'behavior' && (
      <Card className="grid gap-4 p-5">
        <h2 className="text-sm font-semibold">Поведение приложения</h2>
        <div className="grid gap-3 sm:grid-cols-2">
          <Toggle label="Не ограничивать трафик (No-Limit)" checked={h.noLimit} onChange={(v) => onChange({ noLimit: v })} />
          <Toggle label="Всегда передавать HWID устройства" checked={h.alwaysHwid} onChange={(v) => onChange({ alwaysHwid: v })} />
          <Toggle label="Исключить APN из туннеля" checked={h.excludeApns} onChange={(v) => onChange({ excludeApns: v })} />
          <Toggle label="Автоподключение" checked={h.autoConnect} onChange={(v) => onChange({ autoConnect: v })} />
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          {h.autoConnect && (
            <Field label="Что выбирать при автоподключении">
              <Select value={h.autoConnectType || 'lowestdelay'} onChange={(e) => onChange({ autoConnectType: e.target.value })}>
                <option value="lowestdelay">Самый быстрый (минимальная задержка)</option>
                <option value="lastused">Последний использованный</option>
              </Select>
            </Field>
          )}
          <Field label="Способ проверки пинга">
            <Select value={h.pingType} onChange={(e) => onChange({ pingType: e.target.value })}>
              <option value="">Не менять</option>
              <option value="proxy">Через прокси (HTTP)</option>
              <option value="tcp">TCP</option>
              <option value="icmp">ICMP</option>
            </Select>
          </Field>
          <Field label="Режим TUN">
            <Select value={h.tunMode} onChange={(e) => onChange({ tunMode: e.target.value })}>
              <option value="">Не менять</option>
              <option value="tun">TUN</option>
              <option value="proxy">Только прокси</option>
            </Select>
          </Field>
          {text('tunType', 'Тип TUN', 'Например: system, gvisor, mixed')}
          {text('colorProfile', 'Цветовое оформление профиля', 'Значение для Color-Profile')}
          <Field label="Защита локальных прокси (SOCKS/HTTP)" hint="Без пароля любое приложение на устройстве может подключиться к туннелю.">
            <Select value={h.localProxyAuth} onChange={(e) => onChange({ localProxyAuth: e.target.value })}>
              <option value="">Не менять</option>
              <option value="auto">Автоматически</option>
              <option value="off">Выключена</option>
            </Select>
          </Field>
        </div>
        <Field label="Исключённые маршруты" hint="Подсети, которые не идут в туннель, через запятую.">
          <Input value={h.excludeRoutes} onChange={(e) => onChange({ excludeRoutes: e.target.value })} placeholder="10.0.0.0/8, 192.168.0.0/16" />
        </Field>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Проксирование по приложениям">
            <Select value={h.perAppMode || 'off'} onChange={(e) => onChange({ perAppMode: e.target.value })}>
              <option value="off">Не менять</option>
              <option value="on">Только выбранные приложения</option>
              <option value="bypass">Все, кроме выбранных</option>
            </Select>
          </Field>
          {(h.perAppMode === 'on' || h.perAppMode === 'bypass') && (
            <Field label="Список приложений" hint="Идентификаторы пакетов, по одному в строке или через запятую.">
              <Textarea rows={3} value={h.perAppList} onChange={(e) => onChange({ perAppList: e.target.value })} spellCheck={false} />
            </Field>
          )}
        </div>
      </Card>
      )}

      {section === 'routing' && (
      <Card className="p-5">
        <h2 className="mb-3 text-sm font-semibold">Маршрутизация</h2>
        <HappRoutingEditor enabled={h.enableRouting} value={h.routingRules} onEnabled={(v) => onChange({ enableRouting: v })} onChange={(v) => onChange({ routingRules: v })} />
      </Card>
      )}
    </SectionTabs>
  )
}
