import type { ReactNode } from 'react'
import { PageHeader as KitHeader } from '@/ui/kit'

/** Page title row; the subtitle becomes an info tooltip beside the title. */
export function PageHeader({ title, subtitle, actions, eyebrow }: {
  title: ReactNode
  subtitle?: ReactNode
  actions?: ReactNode
  eyebrow?: ReactNode
}) {
  return <KitHeader title={title} info={subtitle} actions={actions} breadcrumb={eyebrow} />
}
