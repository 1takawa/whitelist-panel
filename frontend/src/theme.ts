import { useLayoutEffect, useState } from 'react'

export type Theme = 'light' | 'dark'

const KEY = 'ap-theme'

function systemTheme(): Theme {
  return matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function savedTheme(): Theme | null {
  try {
    const v = localStorage.getItem(KEY)
    return v === 'light' || v === 'dark' ? v : null
  } catch {
    return null
  }
}

// Applied before React renders, so a chosen theme never flashes the system one first. Without a choice the CSS follows
// the system setting on its own.
export function applySavedTheme() {
  const t = savedTheme()
  if (t) document.documentElement.setAttribute('data-theme', t)
}

export function useTheme() {
  const [theme, setTheme] = useState<Theme>(() => savedTheme() ?? systemTheme())
  useLayoutEffect(() => {
    document.documentElement.setAttribute('data-theme', theme)
  }, [theme])
  const toggle = () => {
    const next: Theme = theme === 'dark' ? 'light' : 'dark'
    setTheme(next)
    try {
      localStorage.setItem(KEY, next)
    } catch {
      /* private mode: the choice just is not remembered */
    }
  }
  return { theme, toggle }
}
