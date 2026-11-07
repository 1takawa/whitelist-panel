const UNITS = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ']

export function formatBytes(n: number): string {
  let v = n
  let i = 0
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024
    i++
  }
  return `${i === 0 ? v : v.toFixed(v < 10 ? 2 : 1)} ${UNITS[i]}`
}

export function formatDate(iso: string | null): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('ru-RU', { dateStyle: 'medium', timeStyle: 'short' })
}

// How long ago, in words: «только что», «5 мин назад», «17 ч назад», «3 дн. назад».
export function agoText(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000))
  if (s < 60) return 'только что'
  if (s < 3600) return `${Math.floor(s / 60)} мин назад`
  if (s < 86400) return `${Math.floor(s / 3600)} ч назад`
  return `${Math.floor(s / 86400)} дн. назад`
}

export function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return [d && `${d} д`, h && `${h} ч`, `${m} мин`].filter(Boolean).join(' ')
}

const ALNUM = 'abcdefghijklmnopqrstuvwxyz0123456789'

export function randomString(n: number): string {
  const bytes = crypto.getRandomValues(new Uint8Array(n))
  return Array.from(bytes, (b) => ALNUM[b % ALNUM.length]).join('')
}

export function randomPort(): number {
  const [v] = crypto.getRandomValues(new Uint16Array(1))
  return 10000 + (v % 50000)
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}
