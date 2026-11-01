import { useRef, useState, type FormEvent } from 'react'
import { Navigate } from 'react-router'
import { ApiError } from '../api'
import { useAuth } from '../auth'
import { Input } from '../components/ui'

export default function Login() {
  const { user, login } = useAuth()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const box = useRef<HTMLDivElement>(null)

  if (user) return <Navigate to="/" replace />

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await login(username, password)
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 429
          ? 'Слишком много попыток. Подождите минуту.'
          : err instanceof ApiError && err.status === 401
            ? 'Неверный логин или пароль.'
            : 'Не удалось войти. Попробуйте ещё раз.',
      )
      box.current?.animate([{ transform: 'translateX(0)' }, { transform: 'translateX(-7px)' }, { transform: 'translateX(6px)' }, { transform: 'translateX(-4px)' }, { transform: 'translateX(2px)' }, { transform: 'translateX(0)' }], { duration: 400, easing: 'cubic-bezier(.32,.72,0,1)' })
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="relative grid min-h-full place-items-center overflow-hidden px-4">
      <div aria-hidden="true" className="pointer-events-none absolute -top-52 left-1/2 size-[640px] -translate-x-1/2 rounded-full bg-[radial-gradient(closest-side,var(--brand-soft),transparent)] [animation:ap-drift_14s_ease-in-out_infinite_alternate]" />
      <div ref={box} className="ap-enter relative w-full max-w-[360px] text-center">
        <div className="mx-auto mb-5 grid size-12 place-items-center rounded-[15px] bg-accent text-xl font-bold text-accent-ink">W</div>
        <h1 className="text-2xl font-bold tracking-[-0.03em]">Whitelist Panel</h1>
        <form onSubmit={submit} className="mt-7 grid gap-3 text-left">
          <label className="grid gap-1.5 text-[13px] font-medium" htmlFor="username">
            Логин
            <Input id="username" autoComplete="username" autoFocus required value={username} onChange={(e) => setUsername(e.target.value)} className="h-10" />
          </label>
          <label className="grid gap-1.5 text-[13px] font-medium" htmlFor="password">
            Пароль
            <Input id="password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} className="h-10" />
          </label>
          <p role="alert" className="min-h-[18px] text-center text-[13px] text-blocked">{error}</p>
          <button type="submit" disabled={busy} className="h-10 rounded-[10px] bg-accent text-sm font-medium text-accent-ink hover:opacity-85 disabled:opacity-60">
            {busy ? 'Входим…' : 'Войти'}
          </button>
        </form>
      </div>
    </main>
  )
}
