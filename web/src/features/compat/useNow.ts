import { useEffect, useState } from 'react'
import { serverNow } from '@/lib/format'

/** Server-aligned "now", re-rendered every interval (for countdowns). */
export function useNow(intervalMs = 1000): number {
  const [now, setNow] = useState(() => serverNow())
  useEffect(() => {
    const t = setInterval(() => setNow(serverNow()), intervalMs)
    return () => clearInterval(t)
  }, [intervalMs])
  return now
}
