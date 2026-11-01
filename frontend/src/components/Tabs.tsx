import type { ReactNode } from 'react'
import { Segmented } from './ui'

export type TabDef = { id: string; label: string }

// Accessible tab strip: a sliding pill, arrow keys move between tabs, Home/End jump to the first/last.
export function Tabs({ tabs, active, onChange, children }: { tabs: TabDef[]; active: string; onChange: (id: string) => void; children: ReactNode }) {
  return (
    <div>
      <Segmented items={tabs} value={active} onChange={onChange} label="Разделы" panels className="mb-5" />
      <div key={active} role="tabpanel" id={`panel-${active}`} aria-labelledby={`tab-${active}`} className="ap-enter">
        {children}
      </div>
    </div>
  )
}
