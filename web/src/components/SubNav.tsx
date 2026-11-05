import { LinkTabs } from '@/ui/kit'

/** Section navigation rendered as tab-styled links (each link is a route). */
export function SubNav({ label, items }: { label: string; items: { to: string; label: string; exact?: boolean }[] }) {
  return <LinkTabs label={label} items={items} />
}
