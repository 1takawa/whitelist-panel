import type { Tone } from './components/ui'
import type { Client } from './types'

// A client counts as online when it moved traffic within this window (the server uses the same two minutes).
const ONLINE_WINDOW_MS = 2 * 60 * 1000

export type StateKey = 'active' | 'trial' | 'expired' | 'blocked'

export type ClientState = {
  key: StateKey
  label: string
  tone: Tone
  online: boolean
  // Lower sorts first: online, active, test, expired, blocked.
  rank: number
}

export const stateTone: Record<StateKey, Tone> = { active: 'info', trial: 'trial', expired: 'warn', blocked: 'bad' }

export function clientState(c: Client, now = Date.now()): ClientState {
  const exhausted = c.totalBytes > 0 && c.up + c.down >= c.totalBytes
  const expired = !!c.expiryAt && new Date(c.expiryAt).getTime() <= now
  let key: StateKey
  let label: string
  if (!c.enable) {
    key = 'blocked'
    label = 'Заблокирован'
  } else if (expired) {
    key = 'expired'
    label = 'Истёк'
  } else if (exhausted) {
    key = 'expired'
    label = 'Лимит'
  } else if (c.trial) {
    key = 'trial'
    label = 'Тест'
  } else {
    key = 'active'
    label = 'Активен'
  }
  const usable = key === 'active' || key === 'trial'
  const online = usable && !!c.lastActiveAt && now - new Date(c.lastActiveAt).getTime() <= ONLINE_WINDOW_MS
  const rank = online ? 0 : key === 'active' ? 1 : key === 'trial' ? 2 : key === 'expired' ? 3 : 4
  return { key, label, tone: stateTone[key], online, rank }
}
