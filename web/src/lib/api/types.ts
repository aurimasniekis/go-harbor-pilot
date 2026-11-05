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

// ---------- managed configuration ----------
export type SecretInfo = S['SecretInfo']
export type ProviderSpec = S['ProviderSpec']
export type ModelSpec = S['ModelSpec']
export type ModelPricing = S['ModelPricing']
export type AccountSpec = S['AccountSpec']
export type EntitlementSpec = S['EntitlementSpec']
export type WindowSpec = S['WindowSpec']
export type PoolSpec = S['PoolSpec']
export type PoolMember = S['PoolMember']
export type AliasSpec = S['AliasSpec']
export type PlanSpec = S['PlanSpec']
export type BindingSpec = S['BindingSpec']
export type WorkspaceSpec = S['WorkspaceSpec']
export type ProjectSpec = S['ProjectSpec']
export type PlanDocument = S['PlanDocument']
export type ModelMapping = S['ModelMapping']
export type FallbackPolicy = S['FallbackPolicy']

export type ProviderRes = S['ResourceProviderSpec']
export type AccountRes = S['ResourceAccountSpec']
export type EntitlementRes = S['ResourceEntitlementSpec']
export type PoolRes = S['ResourcePoolSpec']
export type AliasRes = S['ResourceAliasSpec']
export type PlanRes = S['ResourcePlanSpec']
export type BindingRes = S['ResourceBindingSpec']
export type WorkspaceRes = S['ResourceWorkspaceSpec']
export type ProjectRes = S['ResourceProjectSpec']

export type PlanVersion = S['Version']
export type ValidationReport = S['ValidationReport']
export type Credential = S['Credential']
export type IssuedCredential = S['IssuedCredential']
export type Effective = S['EffectiveDTO']

/** Common shape of every managed resource returned by the API. */
export interface ResourceMeta {
  id: string
  kind: string
  name: string
  revision: number
  managed_by: string
  drift_state: 'in_sync' | 'drifted'
  drifted_at?: string
  drifted_by?: string
  updated_at: string
  secrets?: Record<string, SecretInfo>
}
