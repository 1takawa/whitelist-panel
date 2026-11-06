import { Alert, Button, Card, Field, Input, Toggle } from '../../components/ui'
import type { AutoNodeSettings } from '../../types'

const presets: Array<{ label: string; hint: string; intervalSec: number; timeoutSec: number }> = [
  { label: 'Быстро', hint: 'проверка каждые 2 с', intervalSec: 2, timeoutSec: 1 },
  { label: 'Обычно', hint: 'каждые 5 с', intervalSec: 5, timeoutSec: 2 },
  { label: 'Экономно', hint: 'каждые 15 с', intervalSec: 15, timeoutSec: 3 },
]

// "Automatic node": one entry in the subscription that holds all the servers; the app picks the fastest and moves on
// when it stops answering. Only the Xray JSON and Clash formats can carry such a group.
export default function AutoNodeTab({ value: a, onChange, jsonEnabled, clashEnabled }: { value: AutoNodeSettings; onChange: (patch: Partial<AutoNodeSettings>) => void; jsonEnabled: boolean; clashEnabled: boolean }) {
  const num = (key: 'intervalSec' | 'timeoutSec' | 'toleranceMs', label: string, hint: string, min: number, max: number) => (
    <Field label={label} hint={hint}>
      <Input type="number" min={min} max={max} value={a[key]} onChange={(e) => onChange({ [key]: Number(e.target.value) || 0 } as Partial<AutoNodeSettings>)} />
    </Field>
  )
  // The check settings are shared with the extra automatic nodes (the Connections page), so they stay while any of them exists.
  const groups = (a.groups ?? []).length
  const on = a.enable || groups > 0
  return (
    <div className="grid gap-5">
      <Card className="grid gap-4 p-5">
        <h2 className="text-sm font-semibold">Автоузел</h2>
        <p className="text-sm text-dim">
          Одна запись в подписке, внутри которой все серверы клиента. Приложение само выбирает сервер с наименьшей задержкой и, если он перестал отвечать, переходит на следующий по скорости.
          Выбирает именно приложение: только оно знает, что быстрее оттуда, где находится клиент.
        </p>
        <Toggle label="Добавлять автоузел в подписку" checked={a.enable} onChange={(v) => onChange({ enable: v })} />
        <p className="text-xs text-dim">
          Этот автоузел собирает все серверы клиента. Узлов можно сделать несколько, каждому со своим названием, флагом и набором серверов (например, только российские): кнопка «Автоузел» на странице «Подключения».
          {groups > 0 && ` Сейчас дополнительных узлов: ${groups}. Проверка серверов у всех узлов общая, ниже.`}
        </p>
        {on && (
          <>
            <Toggle label="Не переключать Happ и INCY на JSON (автоузла у них тогда не будет)" checked={a.keepLinks} onChange={(v) => onChange({ keepLinks: v })} />
            {a.keepLinks ? (
              <Alert tone="warn">
                Happ и INCY останутся на обычном списке ссылок: в нём группу передать нельзя, автоузла в их списке серверов не будет.
                {!jsonEnabled && !clashEnabled && ' Другие приложения получат его только из адресов форматов «Xray JSON» и «Clash» (вкладка «Форматы»).'}
              </Alert>
            ) : (
              <Alert tone="ok">
                Happ и INCY получают автоузел сами: на основном адресе подписки им отдаётся формат Xray JSON (определяется по User-Agent), отдельный адрес настраивать не нужно.
                Остальные приложения получат его только из адресов форматов «Xray JSON» и «Clash». Как именно приложение покажет такую запись, проверяйте на устройстве.
              </Alert>
            )}
          </>
        )}
      </Card>

      {on && (
        <>
          {a.enable && (
          <Card className="grid gap-4 p-5">
            <h2 className="text-sm font-semibold">Запись в подписке</h2>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Название" hint="Так запись называется в списке серверов приложения">
                <Input value={a.name} maxLength={60} onChange={(e) => onChange({ name: e.target.value })} />
              </Field>
              <p className="self-end pb-2 text-sm text-dim">Место в списке задаётся перетаскиванием на странице «Подключения».</p>
            </div>
            <p className="text-xs text-dim">
              В автоузел входят все включённые серверы клиента. Исключить подключение можно в его настройках («Основное» → «Не включать в автоузел»); оно останется отдельной записью.
              Если подходящих серверов меньше двух, запись не добавляется.
            </p>
          </Card>
          )}

          <Card className="grid gap-4 p-5">
            <h2 className="text-sm font-semibold">Скорость переключения</h2>
            <div className="flex flex-wrap gap-2">
              {presets.map((p) => (
                <Button key={p.label} variant={a.intervalSec === p.intervalSec ? 'primary' : 'default'} onClick={() => onChange({ intervalSec: p.intervalSec, timeoutSec: p.timeoutSec })}>
                  {p.label} · {p.hint}
                </Button>
              ))}
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              {num('intervalSec', 'Интервал проверки, с', 'Как часто приложение проверяет все серверы. От 2 до 3600.', 2, 3600)}
              {num('timeoutSec', 'Таймаут проверки, с (Clash)', 'Дольше этого ответ считается неудачным. Xray берёт своё значение, в нём это не настраивается.', 1, 60)}
              {num('toleranceMs', 'Допуск по задержке, мс (Clash)', 'Текущий сервер заменяется, только если другой быстрее на столько. Не даёт прыгать между почти равными.', 0, 5000)}
              <Field label="Адрес проверки" hint="Любая страница, отвечающая быстро и без содержимого">
                <Input value={a.probeUrl} onChange={(e) => onChange({ probeUrl: e.target.value })} placeholder="https://www.gstatic.com/generate_204" />
              </Field>
            </div>
            <p className="text-xs text-dim">
              Упавший сервер замечается примерно за один интервал, изредка за два (на настоящем Xray при интервале 2 с трафик возвращался за 1–2 с, при 5 с — обычно за 2–5 с).
              Установленные соединения при переключении рвутся, новые идут через другой сервер. Чем короче интервал, тем больше мелких запросов уходит с устройства
              (по одному на каждый сервер за интервал): на телефоне это расход батареи и трафика.
            </p>
          </Card>
        </>
      )}
    </div>
  )
}
