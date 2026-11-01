import type { ReactNode } from 'react'
import { Segmented } from './ui'

export type Section = { id: string; label: string }

// A pill switcher that shows one section of a long settings tab at a time, so the page does not have to be scrolled
// from top to bottom. The buttons are type="button": they sit inside a <form> and must not submit it.
export function SectionTabs({ sections, active, onChange, children }: { sections: Section[]; active: string; onChange: (id: string) => void; children: ReactNode }) {
  return (
    <div className="grid gap-4">
      <Segmented items={sections} value={active} onChange={onChange} label="Разделы настроек" />
      <div key={active} className="ap-enter">{children}</div>
    </div>
  )
}
