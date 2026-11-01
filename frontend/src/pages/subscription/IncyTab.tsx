import { useState } from 'react'
import RoutingPresetPicker from '../../components/RoutingPresetPicker'
import { SectionTabs } from '../../components/SectionTabs'
import { Alert, Card, Field, Input, Select, Textarea, Toggle } from '../../components/ui'
import type { IncySettings } from '../../types'

// INCY switches have three states: leave the app's own setting alone, force on, force off.
function Tri({ label, value, onChange, hint }: { label: string; value: string; onChange: (v: string) => void; hint?: string }) {
  return (
    <Field label={label} hint={hint}>
      <Select value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">Не менять</option>
        <option value="1">Включить</option>
        <option value="0">Выключить</option>
      </Select>
    </Field>
  )
}

const SECTIONS = [
  { id: 'main', label: 'Основное' },
  { id: 'banner', label: 'Баннер' },
  { id: 'behavior', label: 'Поведение' },
  { id: 'bypass', label: 'Обход блокировок' },
  { id: 'routing', label: 'Маршрутизация' },
]

export default function IncyTab({ value: c, onChange }: { value: IncySettings; onChange: (patch: Partial<IncySettings>) => void }) {
  const text = (key: keyof IncySettings, label: string, hint?: string, placeholder?: string) => (
    <Field label={label} hint={hint}>
      <Input value={c[key] as string} placeholder={placeholder} onChange={(e) => onChange({ [key]: e.target.value } as Partial<IncySettings>)} />
    </Field>
  )
  const tri = (key: keyof IncySettings, label: string, hint?: string) => <Tri label={label} value={c[key] as string} hint={hint} onChange={(v) => onChange({ [key]: v } as Partial<IncySettings>)} />

  const [section, setSection] = useState('main')
  return (
    <SectionTabs sections={SECTIONS} active={section} onChange={setSection}>
      {section === 'main' && (
      <Card className="grid gap-4 p-5">
        <div>
          <Toggle label="Управлять приложением INCY" checked={c.autoDetect} onChange={(v) => onChange({ autoDetect: v })} />
          <p className="mt-1 text-xs text-dim">
            Заголовки отправляются только INCY (по User-Agent). Пустые поля и «Не менять» не отправляют ничего, поэтому настройки приложения у пользователя остаются как есть.
          </p>
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          {text('supportEmail', 'E-mail поддержки', undefined, 'help@example.com')}
          <Field label="Сортировка серверов">
            <Select value={c.sortOrder} onChange={(e) => onChange({ sortOrder: e.target.value })}>
              <option value="">Не менять</option>
              <option value="none">Как в подписке</option>
              <option value="ping">По пингу</option>
              <option value="name">По названию</option>
            </Select>
          </Field>
          {text('announceUrl', 'Ссылка на объявление', undefined, 'https://')}
          {text('premiumUrl', 'Ссылка на покупку / продление', undefined, 'https://')}
        </div>
        <Field label="Описание профиля" hint="Русский текст передаётся в base64 автоматически.">
          <Textarea rows={2} value={c.profileDescription} onChange={(e) => onChange({ profileDescription: e.target.value })} spellCheck={false} />
        </Field>
      </Card>
      )}

      {section === 'banner' && (
      <Card className="grid gap-4 p-5">
        <h2 className="text-sm font-semibold">Баннер</h2>
        <div className="grid gap-3 sm:grid-cols-2">
          {text('bannerText', 'Текст баннера')}
          {text('bannerButtonText', 'Текст кнопки')}
          {text('bannerButtonUrl', 'Ссылка кнопки', undefined, 'https://')}
          {text('bannerBgColor', 'Цвет фона', 'Например #1a2b3c', '#')}
          {text('bannerButtonColor', 'Цвет кнопки', 'Например #ff8800', '#')}
        </div>
      </Card>
      )}

      {section === 'behavior' && (
      <Card className="grid gap-4 p-5">
        <h2 className="text-sm font-semibold">Поведение приложения</h2>
        <div className="grid gap-3 sm:grid-cols-3">
          {tri('hideUrl', 'Скрывать адрес подписки')}
          {tri('hideCheck', 'Скрывать проверку')}
          {tri('noLimitEnabled', 'Не ограничивать трафик')}
        </div>
        <div className="grid gap-3 sm:grid-cols-3">
          {tri('perAppEnable', 'Проксирование по приложениям')}
          <Field label="Режим">
            <Select value={c.perAppMode} onChange={(e) => onChange({ perAppMode: e.target.value })}>
              <option value="">Не менять</option>
              <option value="proxy">Только выбранные</option>
              <option value="bypass">Все, кроме выбранных</option>
            </Select>
          </Field>
        </div>
        <Field label="Список приложений" hint="Идентификаторы пакетов, по одному в строке или через запятую.">
          <Textarea rows={3} value={c.perAppList} onChange={(e) => onChange({ perAppList: e.target.value })} spellCheck={false} />
        </Field>
      </Card>
      )}

      {section === 'bypass' && (
      <Card className="grid gap-4 p-5">
        <h2 className="text-sm font-semibold">Обход блокировок</h2>
        <div className="grid gap-3 sm:grid-cols-4">
          {tri('fragmentationEnable', 'Фрагментация')}
          {text('fragmentationLength', 'Длина', 'Число или диапазон', '100-200')}
          {text('fragmentationInterval', 'Интервал, мс', 'Число или диапазон', '10-20')}
          {text('fragmentationPackets', 'Пакеты', 'tlshello, all, 1 или диапазон', 'tlshello')}
        </div>
        <div className="grid gap-3 sm:grid-cols-4">
          {tri('noisesEnable', 'Шум (noises)')}
          <Field label="Тип">
            <Select value={c.noisesType} onChange={(e) => onChange({ noisesType: e.target.value })}>
              <option value="">Не менять</option>
              <option value="rand">rand</option>
              <option value="str">str</option>
              <option value="hex">hex</option>
            </Select>
          </Field>
          {text('noisesPacket', 'Пакет', 'Размер или содержимое', '10-20')}
          {text('noisesDelay', 'Задержка, мс', 'Число или диапазон', '10-16')}
        </div>
        <div className="grid gap-3 sm:grid-cols-3">
          {tri('resolveEnable', 'Разрешать адрес сервера через DNS')}
          {text('resolveDnsDomain', 'DNS-домен', 'Например dns.google', 'dns.google')}
          {text('resolveDnsIp', 'DNS-адрес', undefined, '8.8.8.8')}
        </div>
      </Card>
      )}

      {section === 'routing' && (
      <Card className="grid gap-3 p-5">
        <h2 className="text-sm font-semibold">Маршрутизация</h2>
        <Toggle label="Отправлять маршрутизацию INCY" checked={c.enableRouting} onChange={(v) => onChange({ enableRouting: v })} />
        <RoutingPresetPicker app="incy" current={c.routingRules} onPick={(link) => onChange({ routingRules: link })} />
        <Field label="Ссылка маршрутизации" hint="Одна ссылка в одну строку (например incy://routing/…). INCY читает её из строки в теле подписки, поэтому остальные приложения её не видят.">
          <Input value={c.routingRules} onChange={(e) => onChange({ routingRules: e.target.value })} spellCheck={false} placeholder="incy://routing/…" />
        </Field>
        {c.enableRouting && !c.routingRules.trim() && <Alert tone="warn">Маршрутизация включена, но ссылка не задана: сохранить не получится.</Alert>}
      </Card>
      )}
    </SectionTabs>
  )
}
