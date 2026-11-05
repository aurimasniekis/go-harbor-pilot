/**
 * Named aliases over the generated OpenAPI schema (schema.gen.ts is produced
 * by `make gen-web` from api/openapi.yaml and must not be edited by hand).
 */
import type { components } from './schema.gen'

type S = components['schemas']

export type Session = S['SessionDTO']
export type User = S['UserDTO']
export type WebSession = S['WebSessionDTO']
export type ServicePrincipal = S['ServicePrincipalDTO']
export type ApiToken = S['APITokenDTO']
export type OIDCProvider = S['OIDCProviderDTO']
export type AuthSettings = S['AuthSettingsDTO']
export type SecurityAlert = S['SecurityAlertDTO']
export type AuditEntry = S['AuditEntryDTO']
export type Meta = S['MetaDTO']
export type SystemStatus = S['StatusDTO']

export interface Page<T> {
  items: T[] | null
  next_cursor?: string
}

/** Shared domain enums used by quota and health views. */
export type Health = 'healthy' | 'degraded' | 'auth_failed' | 'stale' | 'error' | 'disabled' | 'pending' | 'unsupported'
export type Availability = 'usable' | 'exhausted' | 'model_limited' | 'unknown'
export type Tier = 'official' | 'partial' | 'unofficial' | 'client_reported' | 'demo' | 'local'
