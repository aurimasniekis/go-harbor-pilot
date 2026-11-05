import { useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiSend, setCsrfToken } from './api/client'
import { qk, sessionQuery } from './api/queries'
import type { Session } from './api/types'

/** Keeps the in-memory CSRF token in sync with the session. */
export function useSessionSync(): Session | undefined {
  const { data } = useQuery(sessionQuery)
  useEffect(() => {
    setCsrfToken(data?.csrf_token)
  }, [data?.csrf_token])
  return data
}

/** True when the signed-in principal holds the permission. */
export function useCan(permission: string): boolean {
  const { data } = useQuery(sessionQuery)
  return data?.permissions?.includes(permission) ?? false
}

function applySession(qc: ReturnType<typeof useQueryClient>, s: Session) {
  setCsrfToken(s.csrf_token)
  qc.setQueryData(qk.session, s)
  void qc.invalidateQueries({ predicate: (q) => q.queryKey[0] !== 'session' })
}

export function useLogin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { email: string; password: string }) => apiSend<Session>('POST', '/session', v),
    onSuccess: (s) => applySession(qc, s),
  })
}

export function useSetup() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { token: string; email: string; display_name: string; password: string }) => apiSend<Session>('POST', '/auth/setup', v),
    onSuccess: (s) => applySession(qc, s),
  })
}

export function useRecover() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (token: string) => apiSend<Session>('POST', '/auth/recover', { token }),
    onSuccess: (s) => applySession(qc, s),
  })
}

export function useAccept() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { token: string; purpose: 'invite' | 'reset'; password: string }) => apiSend<Session>('POST', '/auth/accept', v),
    onSuccess: (s) => applySession(qc, s),
  })
}

export function useLogout() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => apiSend<Session>('DELETE', '/session'),
    onSettled: () => {
      setCsrfToken(undefined)
      void qc.invalidateQueries()
    },
  })
}
