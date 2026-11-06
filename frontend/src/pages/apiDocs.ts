// Reference for the REST API that an API token can use. The same routes serve the web UI, so the shapes match
// internal/model and frontend/src/types.ts. Routes that need a panel session (token management, Telegram,
// server install) are deliberately left out: a token cannot call them.

export type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export type DocField = { name: string; type: string; required?: boolean; desc: string }

export type Endpoint = {
  method: Method
  path: string
  title: string
  desc: string
  // public: no Authorization header needed
  public?: boolean
  params?: DocField[] // path and query parameters
  body?: DocField[]
  request?: string // example JSON body
  status?: string
  response: string
  note?: string
}

export type DocGroup = { id: string; title: string; intro?: string; endpoints: Endpoint[] }

const client = `{
  "id": "6650f1c2a1b2c3d4e5f60718",
  "email": "client_123",
  "subId": "k3Jf9sLq2xVbT7aQ",
  "uuid": "8f0d6c1e-3b5a-4c7e-9d21-5a6b7c8d9e0f",
  "password": "Xk29sLq0aPz41mNc",
  "flow": "",
  "enable": true,
  "totalBytes": 53687091200,
  "expiryAt": "2026-12-01T00:00:00Z",
  "inboundIds": ["6650f0aa11b2c3d4e5f60001"],
  "up": 1048576,
  "down": 5242880,
  "comment": "order #123",
  "createdAt": "2026-10-28T02:40:12Z",
  "lastActiveAt": null,
  "telegramId": 0
}`

const inbound = `{
  "id": "6650f0aa11b2c3d4e5f60001",
  "remark": "reality-main",
  "enable": true,
  "protocol": "vless",
  "listen": "0.0.0.0",
  "port": 8443,
  "network": "tcp",
  "security": "reality",
  "reality": {
    "dest": "127.0.0.1:7443",
    "serverNames": ["example.com"],
    "privateKey": "…",
    "publicKey": "…",
    "shortIds": ["a1b2c3d4"],
    "spiderX": ""
  },
  "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"], "routeOnly": false },
  "public": { "host": "", "port": 443, "security": "", "sni": "", "fingerprint": "firefox", "alpn": "" },
  "acceptProxyProtocol": false,
  "serverId": "",
  "createdAt": "2026-10-28T02:40:12Z",
  "clients": 3
}`

const clientFields: DocField[] = [
  { name: 'email', type: 'string', required: true, desc: 'Имя клиента: 1–64 символа, буквы, цифры и _ . @ + -. По нему считается трафик.' },
  { name: 'inboundIds', type: 'string[]', desc: 'Идентификаторы подключений, к которым привязан клиент.' },
  { name: 'enable', type: 'boolean', desc: 'false — клиент отключён, подключиться не может.' },
  { name: 'totalBytes', type: 'integer', desc: 'Лимит трафика в байтах. 0 — без ограничения.' },
  { name: 'expiryAt', type: 'string | null', desc: 'Конец подписки, RFC 3339 (UTC). null — бессрочно.' },
  { name: 'flow', type: 'string', desc: 'Пусто или xtls-rprx-vision (только VLESS + TCP).' },
  { name: 'uuid', type: 'string', desc: 'UUID для VLESS. Пусто — создаётся автоматически, при изменении остаётся прежним.' },
  { name: 'password', type: 'string', desc: 'Пароль для Trojan. Пусто — создаётся автоматически, при изменении остаётся прежним.' },
  { name: 'subId', type: 'string', desc: 'Идентификатор подписки: 4–64 символа, буквы, цифры, _ -. Пусто — создаётся автоматически. Меняя его, вы делаете старую ссылку недействительной.' },
  { name: 'comment', type: 'string', desc: 'Заметка до 256 символов (например, номер заказа).' },
]

const idParam = (what: string): DocField[] => [{ name: 'id', type: 'path string', required: true, desc: `Идентификатор ${what}.` }]

export const docGroups: DocGroup[] = [
  {
    id: 'service',
    title: 'Служебные',
    endpoints: [
      {
        method: 'GET',
        path: '/api/health',
        title: 'Проверить API',
        desc: 'Проверяет, что панель отвечает и база данных доступна. Ключ не нужен — удобно для мониторинга.',
        public: true,
        response: `{ "status": "ok" }`,
        note: 'Если база недоступна, вернётся код 503 и {"status": "degraded"}.',
      },
      {
        method: 'GET',
        path: '/api/dashboard',
        title: 'Получить общую статистику',
        desc: 'Нагрузка сервера, состояние Xray, количество подключений и клиентов, суммарный трафик, топ клиентов и график за последние часы.',
        response: `{
  "version": "0.8.0",
  "system": { "hostname": "vps", "uptimeSeconds": 86400, "cpuPercent": 3.2, "memTotal": 2086391808, "memUsed": 612368384, "diskTotal": 21474836480, "diskUsed": 6442450944, "cpus": 2 },
  "xray": { "installed": true, "running": true, "pid": 1234, "version": "26.10.29", "uptimeSeconds": 86000, "lastError": "" },
  "inbounds": { "total": 2, "enabled": 2 },
  "clients": { "total": 20, "active": 17, "online": 4, "expired": 2, "depleted": 1, "disabled": 0 },
  "traffic": { "up": 1048576, "down": 5242880 },
  "top": [{ "name": "client_123", "used": 6291456, "limit": 0 }],
  "history": [{ "time": 1793242800, "up": 1024, "down": 4096 }],
  "onlineNames": ["client_123"]
}`,
      },
      {
        method: 'GET',
        path: '/api/auth/me',
        title: 'Проверить ключ',
        desc: 'Возвращает 200, если ключ действителен.',
        response: `{ "username": "api-token" }`,
        note: 'С неверным ключом вернётся 401.',
      },
    ],
  },
  {
    id: 'clients',
    title: 'Клиенты',
    intro: 'Клиент — это подписчик. Он может быть привязан к нескольким подключениям, а ссылка на подписку у него одна.',
    endpoints: [
      {
        method: 'GET',
        path: '/api/clients',
        title: 'Список клиентов',
        desc: 'Возвращает всех клиентов вместе с учтённым трафиком (up — отдано, down — получено, в байтах).',
        response: `[
${client.replace(/^/gm, '  ')}
]`,
      },
      {
        method: 'POST',
        path: '/api/clients',
        title: 'Создать клиента',
        desc: 'Создаёт клиента и сразу применяет конфигурацию к Xray. Незаданные uuid, password и subId генерируются автоматически.',
        body: clientFields,
        request: `{
  "email": "client_123",
  "inboundIds": ["6650f0aa11b2c3d4e5f60001"],
  "enable": true,
  "totalBytes": 53687091200,
  "expiryAt": "2026-12-01T00:00:00Z",
  "comment": "order #123"
}`,
        response: `{
  "item": ${client.replace(/\n/g, '\n  ')}
}`,
        note: 'Если клиент сохранён, но Xray не принял конфигурацию, в ответе появится поле "warning" с текстом ошибки. Дубликат имени вернёт 409.',
      },
      {
        method: 'GET',
        path: '/api/clients/{id}',
        title: 'Получить клиента',
        desc: 'Возвращает одного клиента по идентификатору.',
        params: idParam('клиента (поле id)'),
        response: client,
      },
      {
        method: 'PUT',
        path: '/api/clients/{id}',
        title: 'Изменить клиента',
        desc: 'Заменяет клиента целиком: сначала получите его через GET, измените нужные поля и отправьте объект обратно. Счётчики трафика сохраняются.',
        params: idParam('клиента'),
        body: clientFields,
        request: `{
  "email": "client_123",
  "inboundIds": ["6650f0aa11b2c3d4e5f60001"],
  "enable": true,
  "totalBytes": 107374182400,
  "expiryAt": "2027-01-01T00:00:00Z",
  "comment": "order #123, продлён"
}`,
        response: `{
  "item": ${client.replace(/\n/g, '\n  ')}
}`,
        note: 'Поля, не включённые в тело, получают значения по умолчанию (enable — false, totalBytes — 0, expiryAt — null), а не сохраняются прежними. Исключение — uuid, password и subId.',
      },
      {
        method: 'DELETE',
        path: '/api/clients/{id}',
        title: 'Удалить клиента',
        desc: 'Удаляет клиента. Сохранённые у него конфиги перестают работать после применения конфигурации.',
        params: idParam('клиента'),
        response: `{}`,
      },
      {
        method: 'POST',
        path: '/api/clients/{id}/reset-traffic',
        title: 'Сбросить трафик',
        desc: 'Обнуляет учтённый трафик клиента. Если подписка была закрыта из-за исчерпанного лимита, она снова станет рабочей.',
        params: idParam('клиента'),
        response: `{
  "item": ${client.replace(/\n/g, '\n  ').replace('"up": 1048576', '"up": 0').replace('"down": 5242880', '"down": 0')}
}`,
      },
      {
        method: 'GET',
        path: '/api/clients/{id}/links',
        title: 'Получить ссылки клиента',
        desc: 'Возвращает адрес подписки и готовые ссылки vless:// и trojan:// для каждого подключения клиента.',
        params: idParam('клиента'),
        response: `{
  "subscriptionUrl": "https://panel.example.com/sub/k3Jf9sLq2xVbT7aQ",
  "links": [
    {
      "inboundId": "6650f0aa11b2c3d4e5f60001",
      "remark": "reality-main",
      "name": "reality-main-client_123|📊50GB|⏳30D",
      "protocol": "vless",
      "network": "tcp",
      "security": "reality",
      "enabled": true,
      "link": "vless://8f0d6c1e-…@example.com:443?security=reality&…#reality-main-client_123"
    }
  ]
}`,
        note: 'Если ссылку для подключения построить не удалось (например, не задан домен), в элементе будет поле "error" вместо рабочего "link".',
      },
    ],
  },
  {
    id: 'subscription',
    title: 'Подписка',
    endpoints: [
      {
        method: 'GET',
        path: '/api/sub/{subId}',
        title: 'Данные публичной страницы подписки',
        desc: 'То, что видит клиент на странице подписки: статус, трафик, срок и список ссылок. Ключ не нужен — достаточно знать subId клиента.',
        public: true,
        params: [{ name: 'subId', type: 'path string', required: true, desc: 'Идентификатор подписки клиента (поле subId).' }],
        response: `{
  "name": "client_123",
  "title": "whitelist",
  "status": "active",
  "up": 1048576,
  "down": 5242880,
  "total": 53687091200,
  "expiryAt": "2026-12-01T00:00:00Z",
  "url": "https://panel.example.com/sub/k3Jf9sLq2xVbT7aQ",
  "supportUrl": "",
  "profileUrl": "",
  "announce": "",
  "links": [{ "remark": "reality-main", "name": "reality-main-client_123", "protocol": "vless", "network": "tcp", "security": "reality", "link": "vless://…" }]
}`,
        note: 'Статус: active, disabled, expired или exhausted. Сам файл подписки для приложений отдаётся по адресу <префикс><subId>, префикс по умолчанию /sub/.',
      },
    ],
  },
  {
    id: 'inbounds',
    title: 'Подключения',
    intro: 'Подключение — это порт Xray с протоколом VLESS или Trojan и выбранным транспортом.',
    endpoints: [
      {
        method: 'GET',
        path: '/api/inbounds',
        title: 'Список подключений',
        desc: 'Возвращает все подключения. В поле clients — число привязанных клиентов.',
        response: `[
${inbound.replace(/^/gm, '  ')}
]`,
      },
      {
        method: 'POST',
        path: '/api/inbounds',
        title: 'Создать подключение',
        desc: 'Создаёт подключение и применяет конфигурацию к Xray. Для REALITY незаданные ключи и shortIds создаются автоматически.',
        body: [
          { name: 'remark', type: 'string', required: true, desc: 'Название, 1–64 символа.' },
          { name: 'protocol', type: 'string', required: true, desc: 'vless или trojan.' },
          { name: 'port', type: 'integer', required: true, desc: 'Порт, 1–65535. Порты самой панели занять нельзя. На одном сервере порт уникален.' },
          { name: 'network', type: 'string', required: true, desc: 'tcp, ws, grpc, xhttp или httpupgrade.' },
          { name: 'security', type: 'string', required: true, desc: 'none, tls или reality. REALITY только для VLESS и сетей tcp, grpc, xhttp.' },
          { name: 'enable', type: 'boolean', desc: 'false — подключение выключено.' },
          { name: 'listen', type: 'string', desc: 'IP-адрес. По умолчанию 0.0.0.0.' },
          { name: 'ws / httpupgrade / xhttp', type: 'object', desc: 'Настройки транспорта: path, host; для xhttp ещё mode (auto, packet-up, stream-up, stream-one).' },
          { name: 'grpc', type: 'object', desc: 'serviceName (обязательно для grpc), authority, multiMode.' },
          { name: 'tls', type: 'object', desc: 'serverName, certFile и keyFile (обязательны при security = tls), alpn.' },
          { name: 'reality', type: 'object', desc: 'dest (host:port), serverNames, privateKey, shortIds, spiderX. publicKey вычисляется из privateKey.' },
          { name: 'sniffing', type: 'object', desc: 'enabled, destOverride (http, tls, quic, fakedns), routeOnly.' },
          { name: 'public', type: 'object', desc: 'Что попадёт в ссылку клиента: host, port, security, sni, fingerprint, alpn. Пустые значения вычисляются из настроек.' },
          { name: 'acceptProxyProtocol', type: 'boolean', desc: 'true, если перед подключением стоит nginx с proxy_protocol.' },
          { name: 'serverId', type: 'string', desc: 'Идентификатор дополнительного сервера. Пусто — этот сервер.' },
        ],
        request: `{
  "remark": "reality-main",
  "enable": true,
  "protocol": "vless",
  "port": 8443,
  "network": "tcp",
  "security": "reality",
  "reality": { "dest": "127.0.0.1:7443", "serverNames": ["example.com"] }
}`,
        response: `{
  "item": ${inbound.replace(/\n/g, '\n  ')}
}`,
        note: 'Ключ REALITY можно получить заранее через GET /api/reality/keys. Ошибки проверки возвращают 400 с понятным текстом.',
      },
      {
        method: 'GET',
        path: '/api/inbounds/{id}',
        title: 'Получить подключение',
        desc: 'Возвращает одно подключение по идентификатору.',
        params: idParam('подключения'),
        response: inbound,
      },
      {
        method: 'PUT',
        path: '/api/inbounds/{id}',
        title: 'Изменить подключение',
        desc: 'Заменяет подключение целиком: получите его через GET, измените поля и отправьте обратно. Привязанные клиенты сохраняются.',
        params: idParam('подключения'),
        request: `{ …полный объект подключения, как в POST… }`,
        response: `{
  "item": ${inbound.replace(/\n/g, '\n  ')}
}`,
      },
      {
        method: 'DELETE',
        path: '/api/inbounds/{id}',
        title: 'Удалить подключение',
        desc: 'Удаляет подключение. Клиенты остаются, но привязка к нему снимается.',
        params: idParam('подключения'),
        response: `{}`,
      },
      {
        method: 'GET',
        path: '/api/reality/keys',
        title: 'Сгенерировать ключи REALITY',
        desc: 'Возвращает новую пару ключей и shortId. Ничего не сохраняет — значения нужно передать при создании подключения.',
        response: `{
  "privateKey": "uFs0…",
  "publicKey": "Qm7x…",
  "shortId": "a1b2c3d4e5f60718"
}`,
      },
    ],
  },
  {
    id: 'xray',
    title: 'Xray',
    endpoints: [
      {
        method: 'GET',
        path: '/api/xray/status',
        title: 'Состояние Xray',
        desc: 'Установлен ли Xray, запущен ли, версия, время работы и последняя ошибка.',
        response: `{
  "installed": true,
  "running": true,
  "pid": 1234,
  "version": "26.10.29",
  "uptimeSeconds": 86000,
  "lastError": ""
}`,
      },
      {
        method: 'POST',
        path: '/api/xray/restart',
        title: 'Перезапустить Xray',
        desc: 'Перезапускает Xray с актуальной конфигурацией. Соединения клиентов на мгновение прервутся.',
        response: `{ "installed": true, "running": true, "pid": 1301, "version": "26.10.29", "uptimeSeconds": 1, "lastError": "" }`,
        note: 'Если Xray не удалось запустить, вернётся 502 с текстом ошибки.',
      },
      {
        method: 'GET',
        path: '/api/xray/logs',
        title: 'Журнал Xray',
        desc: 'Последние строки журнала.',
        params: [{ name: 'n', type: 'query integer', desc: 'Сколько строк вернуть, от 1 до 500. По умолчанию 200.' }],
        response: `{ "lines": ["2026/10/31 12:00:00 [Info] …"] }`,
      },
    ],
  },
  {
    id: 'servers',
    title: 'Серверы',
    intro: 'Дополнительные серверы с Xray, которыми управляет панель. Установка и изменение серверов доступны только из веб-интерфейса.',
    endpoints: [
      {
        method: 'GET',
        path: '/api/servers',
        title: 'Список серверов',
        desc: 'Дополнительные серверы с состоянием, нагрузкой, пингом и числом клиентов.',
        response: `[
  {
    "id": "6650f3bb11b2c3d4e5f60002",
    "name": "Frankfurt",
    "address": "203.0.113.10",
    "port": 8844,
    "enable": true,
    "country": "DE",
    "flag": "🇩🇪",
    "managed": true,
    "inbounds": 1,
    "clients": 12,
    "onlineClients": 3,
    "up": 1048576,
    "down": 5242880,
    "online": true,
    "pingMs": 34,
    "lastSeen": "2026-10-31T12:00:00Z",
    "agentVersion": "0.8.0"
  }
]`,
        note: 'В ответе также есть поля xray и system, как в /api/dashboard. Токены и ключи серверов не возвращаются.',
      },
    ],
  },
  {
    id: 'nodes',
    title: 'Внешние ноды',
    intro: 'Готовые ссылки чужих серверов, которые добавляются в подписку каждого клиента.',
    endpoints: [
      {
        method: 'GET',
        path: '/api/external-nodes',
        title: 'Список внешних нод',
        desc: 'Все внешние ноды с разобранными параметрами.',
        response: `[
  {
    "id": "6650f4cc11b2c3d4e5f60003",
    "name": "NL backup",
    "uri": "vless://…@nl.example.com:443?security=reality#NL",
    "enable": true,
    "createdAt": "2026-10-28T02:40:12Z",
    "protocol": "vless",
    "host": "nl.example.com",
    "port": 443,
    "network": "tcp",
    "security": "reality"
  }
]`,
      },
      {
        method: 'POST',
        path: '/api/external-nodes',
        title: 'Добавить внешнюю ноду',
        desc: 'Добавляет одну ноду по ссылке.',
        body: [
          { name: 'uri', type: 'string', required: true, desc: 'Ссылка vless:// или trojan://. Другие схемы не принимаются.' },
          { name: 'name', type: 'string', desc: 'Название до 100 символов. Пусто — берётся из фрагмента ссылки (#…), иначе host:port.' },
          { name: 'enable', type: 'boolean', desc: 'false — нода не попадает в подписки.' },
        ],
        request: `{
  "name": "NL backup",
  "uri": "vless://…@nl.example.com:443?security=reality#NL",
  "enable": true
}`,
        response: `{ "id": "6650f4cc11b2c3d4e5f60003", "name": "NL backup", "uri": "vless://…", "enable": true, "protocol": "vless", "host": "nl.example.com", "port": 443, "network": "tcp", "security": "reality", "createdAt": "2026-10-28T02:40:12Z" }`,
        note: 'Тот же сервер нельзя добавить дважды (имя при сравнении не учитывается): вернётся 400. Всего не больше 200 нод.',
      },
      {
        method: 'POST',
        path: '/api/external-nodes/import',
        title: 'Импортировать список нод',
        desc: 'Добавляет много нод из текста, по одной ссылке в строке. Нерабочие строки пропускаются и перечисляются в ответе.',
        body: [{ name: 'text', type: 'string', required: true, desc: 'Ссылки по одной в строке.' }],
        request: `{ "text": "vless://…#One\\ntrojan://…#Two" }`,
        response: `{ "added": 2, "problems": [] }`,
        note: 'Пример проблемы: {"line": 3, "error": "…"} — номер строки и причина.',
      },
      {
        method: 'PUT',
        path: '/api/external-nodes/{id}',
        title: 'Изменить внешнюю ноду',
        desc: 'Заменяет название, ссылку и признак включения. Если uri не передан, остаётся прежний.',
        params: idParam('ноды'),
        request: `{ "name": "NL backup", "uri": "vless://…", "enable": false }`,
        response: `{ "id": "6650f4cc11b2c3d4e5f60003", "name": "NL backup", "enable": false, "…": "…" }`,
      },
      {
        method: 'DELETE',
        path: '/api/external-nodes/{id}',
        title: 'Удалить внешнюю ноду',
        desc: 'Удаляет ноду. Из подписок она исчезнет при следующем обновлении клиентом.',
        params: idParam('ноды'),
        response: `{ "status": "ok" }`,
      },
    ],
  },
  {
    id: 'export',
    title: 'Выгрузка данных',
    endpoints: [
      {
        method: 'GET',
        path: '/api/db/export',
        title: 'Выгрузить пользователей',
        desc: 'Отдаёт файл с клиентами для переноса или резервного хранения. Полная выгрузка (kind=full) доступна только из панели, по ключу можно получить только kind=users.',
        params: [{ name: 'kind', type: 'query string', required: true, desc: 'users — только клиенты. Значение full по ключу недоступно.' }],
        status: '200 file',
        response: `(файл выгрузки)`,
        note: 'Пример: curl -OJ -H "Authorization: Bearer ТОКЕН" "…/api/db/export?kind=users". Резервные копии базы больше не хранятся на сервере: ежедневный архив уходит администраторам в Telegram.',
      },
    ],
  },
  {
    id: 'settings',
    title: 'Настройки',
    endpoints: [
      {
        method: 'GET',
        path: '/api/settings',
        title: 'Получить настройки',
        desc: 'Все настройки панели одним документом: домены, подписка и параметры Telegram-бота (без токена бота). Домен панели (`panelDomain`) всегда равен домену, на котором панель работает, и при сохранении подставляется сам.',
        response: `{
  "panelDomain": "panel.example.com",
  "realityDomain": "example.com",
  "subscription": { "enable": true, "path": "/sub/", "title": "whitelist", "…": "…" },
  "telegram": { "enable": false, "adminIds": [] }
}`,
      },
      {
        method: 'PUT',
        path: '/api/settings',
        title: 'Сохранить настройки',
        desc: 'Заменяет документ настроек целиком. Сначала получите его через GET, поменяйте нужное и отправьте обратно.',
        request: `{ …документ из GET /api/settings… }`,
        response: `{ …сохранённые настройки… }`,
        note: 'Пропущенные настройки подписки заменяются значениями по умолчанию, поэтому не отправляйте частичный документ.',
      },
    ],
  },
]
