import { useEffect, useState } from 'react'
import { Button, Modal } from './ui'

type Request = { message: string; confirmLabel: string; danger: boolean; resolve: (ok: boolean) => void }

let show: ((r: Request) => void) | null = null

// ask opens the panel's own confirmation window in place of the browser's confirm(): `if (!(await ask('…'))) return`.
// Without a mounted <ConfirmHost/> it falls back to the browser dialog, so a call never hangs.
export function ask(message: string, opts: { confirmLabel?: string; danger?: boolean } = {}): Promise<boolean> {
  return new Promise((resolve) => {
    if (!show) {
      resolve(window.confirm(message))
      return
    }
    show({ message, confirmLabel: opts.confirmLabel ?? 'Продолжить', danger: opts.danger ?? true, resolve })
  })
}

export function ConfirmHost() {
  const [req, setReq] = useState<Request | null>(null)
  useEffect(() => {
    show = setReq
    return () => {
      show = null
    }
  }, [])
  if (!req) return null
  const done = (ok: boolean) => {
    req.resolve(ok)
    setReq(null)
  }
  return (
    // Closing the window any other way (Esc, the cross, a click outside) counts as "no".
    <Modal title="Подтвердите действие" onClose={() => done(false)}>
      <p className="whitespace-pre-line text-sm leading-relaxed">{req.message}</p>
      <div className="mt-5 flex justify-end gap-2">
        <Button variant="ghost" onClick={() => done(false)}>Отмена</Button>
        <Button variant="primary" className={req.danger ? '!border-blocked !bg-blocked !text-white' : ''} autoFocus onClick={() => done(true)}>{req.confirmLabel}</Button>
      </div>
    </Modal>
  )
}
