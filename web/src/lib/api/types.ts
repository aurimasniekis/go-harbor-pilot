/**
 * Named aliases over the generated OpenAPI schema (schema.gen.ts is produced
 * by `make gen-web` from api/openapi.yaml and must not be edited by hand).
 */
import type { components } from './schema.gen'

type S = components['schemas']

/**
 * Server responses carry every spec field (specs are normalized before they
 * are stored), while the OpenAPI schema marks many as optional for writes.
 * Read-side types are therefore deep-required.
 */
type Filled<T> = T extends (infer U)[] ? Filled<U>[] : T extends object ? { [K in keyof T]-?: Filled<T[K]> } : T

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
export type ProviderSpec = Filled<Omit<S['ProviderSpec'], 'models'>> & { models: ModelSpec[] }
export type ModelSpec = Filled<Omit<S['ModelSpec'], 'pricing' | 'provider_routing' | 'reasoning'>> &
  Pick<S['ModelSpec'], 'pricing' | 'provider_routing' | 'reasoning'>
/** A model's reasoning option (D-047); optional as a whole, absent = the built-in translation. */
export type ModelReasoningSpec = S['ModelReasoningSpec']
export type ModelPricing = S['ModelPricing']
/** Upstream routing pin; optional as a whole so the editor can omit it. */
export type UpstreamProviderSpec = S['UpstreamProviderSpec']
export type UpstreamMaxPrice = S['UpstreamMaxPrice']
export type AccountSpec = Filled<S['AccountSpec']>
export type EntitlementSpec = S['EntitlementSpec']
export type WindowSpec = S['WindowSpec']
export type PoolSpec = Filled<Omit<S['PoolSpec'], 'members'>> & Pick<S['PoolSpec'], 'members'>
export type PoolMember = S['PoolMember']
export type AliasSpec = Filled<S['AliasSpec']>
export type PlanSpec = S['PlanSpec']
export type BindingSpec = Filled<S['BindingSpec']>
export type WorkspaceSpec = Filled<S['WorkspaceSpec']>
export type ProjectSpec = Filled<S['ProjectSpec']>
/** Optional sections stay optional so the editor can omit them. */
type PlanOptional = 'compatibility' | 'engine' | 'intent' | 'privacy'
export type PlanDocument = Filled<Omit<S['PlanDocument'], PlanOptional>> & Pick<S['PlanDocument'], PlanOptional>
export type ModelMapping = S['ModelMapping']
export type FallbackPolicy = Filled<S['FallbackPolicy']>

export type ProviderRes = Filled<S['ResourceProviderSpec']>
export type AccountRes = Filled<S['ResourceAccountSpec']>
export type EntitlementRes = Filled<S['ResourceEntitlementSpec']>
export type PoolRes = Filled<S['ResourcePoolSpec']>
export type AliasRes = Filled<S['ResourceAliasSpec']>
export type PlanRes = Filled<S['ResourcePlanSpec']>
export type BindingRes = Filled<S['ResourceBindingSpec']>
export type WorkspaceRes = Filled<S['ResourceWorkspaceSpec']>
export type ProjectRes = Filled<S['ResourceProjectSpec']>

export type PlanVersion = Filled<S['Version']>
export type ValidationReport = S['ValidationReport']
export type FieldErrorDTO = S['FieldError']
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

// ---------- activity ----------
export type RequestSummary = S['RequestSummary']
export type RequestDetail = S['RequestDetail']
export type Attempt = S['AttemptDTO']
export type LedgerEntry = S['LedgerDTO']
export type ObservedSession = S['ObservedSession']
export type ActivitySummary = S['ActivitySummary']
export type Tokens = S['Tokens']
export type Cost = S['CostDTO']

/** Routing explanation recorded with each request (decision JSON). */
export interface DecisionCandidate {
  provider_id: string
  provider: string
  account_id: string
  account: string
  model: string
  billing: string
  upstream_protocol: string
  native: boolean
  priority: number
  weight: number
  score: number
  reasons: string[] | null
  health?: { Available?: boolean; Reason?: string; Stale?: boolean; Remaining?: number | null; ResetAt?: string | null } | null
}

export interface DecisionExclusion {
  provider_id: string
  account_id: string
  account: string
  model: string
  stage: string
  reason: string
  detail: string
}

export interface Decision {
  requested_model?: string
  alias_id?: string
  alias?: string
  alias_chain?: string[] | null
  resolution?: string
  pool_id?: string
  pool?: string
  strategy?: string
  plan_id?: string
  plan_version?: number
  plan_version_id?: string
  config_revision?: number
  required_capabilities?: string[] | null
  candidates?: DecisionCandidate[] | null
  excluded?: DecisionExclusion[] | null
  notes?: string[] | null
  affinity?: string
  intent?: string
  intent_source?: string
  rules?: RuleTrace[] | null
  mode?: string
}

export interface TranslationLoss {
  feature: string
  detail: string
  severity: 'approximate' | 'drop' | 'reject' | string
}

// ---------- routing engine ----------
// The plan document's `engine` section is declared as free-form JSON in the
// OpenAPI spec (it is decoded strictly by the server), so its shape is
// written out here to match internal/routing/engine.go.

export type RuleStage = 'request' | 'candidate'
export type RuleAction = 'block' | 'require' | 'set_intent' | 'exclude' | 'boost'
export type HitPolicy = 'first' | 'unique' | 'collect'

export interface RuleSpec {
  name: string
  stage: RuleStage
  when: string
  action: RuleAction
  capability?: string
  intent?: string
  boost?: number
  reason?: string
  enabled: boolean
}

export interface TableRow {
  conditions: string[]
  action: RuleAction
  capability?: string
  intent?: string
  boost?: number
  reason?: string
}

export interface TableSpec {
  name: string
  stage: RuleStage
  hit_policy: HitPolicy
  inputs: string[]
  rows: TableRow[]
  enabled: boolean
}

export interface EngineSpec {
  rules: RuleSpec[]
  tables: TableSpec[]
  score: string
}

export type RuleTrace = S['Trace']
export type RoutingVariable = S['Variable']
export type SimulateRequest = S['SimulateRequest']
export type HealthOverride = S['HealthOverride']

/** Simulation result; `decision` uses the hand-written Decision shape shared with activity. */
export interface SimulationResult {
  mode: string
  decision: Decision | null
  error?: string
  error_kind?: string
  version: number
}

export interface ReplayResult {
  recorded: unknown
  replay: SimulationResult
  changed: boolean
  notes: string[]
}

// ---------- quota, usage and budgets ----------
export type QuotaMonitorSpec = S['QuotaMonitorSpec']
export type QuotaMonitorRes = Filled<S['ResourceQuotaMonitorSpec']>
export type BudgetSpec = S['BudgetSpec']
export type BudgetRes = Filled<S['ResourceBudgetSpec']>
export type QuotaBoard = S['BoardBody']
export type MonitorView = S['MonitorView']
export type BucketView = S['BucketView']
export type PollView = S['PollView']
export type AccountQuotaView = S['AccountQuotaView']
export type AccountHealthView = S['AccountHealthView']
export type QuotaAdapter = S['AdapterView']
export type QuotaLogin = S['LoginView']
export type HistoryPoint = S['HistoryPoint']
export type Forecast = S['QuotaForecast']
export type ForecastPoint = S['Point']
export type ExternalUsageRow = S['ExternalUsageRow']
export type ReconciliationRow = S['ReconciliationRow']
export type BudgetUsage = S['BudgetUsage']
export type UsageSummary = S['UsageSummary']
export type UsageGroup = S['UsageGroup']
export type UsageTokens = S['UsageTokens']
export type CostAmount = S['CostAmount']
export type SubscriptionFee = S['SubscriptionFee']
export type UsagePoint = S['UsagePoint']
export type UsageGroupBy = 'account' | 'provider' | 'model' | 'binding' | 'project' | 'workspace' | 'billing'
/** A usage summary grouping: a dimension, or `ref:<key>` for a trusted reference key (paged, D-041). */
export type UsageSummaryGroupBy = UsageGroupBy | `ref:${string}`

// ---------- privacy and traces ----------
export type PrivacyPolicySpec = S['PrivacyPolicySpec']
export type PrivacyPolicyRes = Filled<S['ResourcePrivacyPolicySpec']>
export type PrivacyRule = S['PrivacyRule']
export type PrivacyAllowRule = S['PrivacyAllowRule']
export type ToolArgRestore = S['ToolArgRestore']
export type TracePolicySpec = S['TracePolicySpec']
export type TracePolicyRes = Filled<S['ResourceTracePolicySpec']>
export type PlanPrivacy = S['PlanPrivacy']
export type SandboxResult = S['SandboxResult']
export type Finding = S['Finding']
export type PrivacySummary = S['Summary']
export type TraceMeta = S['Meta']
export type TraceContent = S['TraceContent']
export type TraceHit = S['Hit']
export type TraceMode = TraceMeta['mode']
export type BridgeStatus = S['BridgeStatus']

// ---------- compatibility and Protocol Watch ----------
export type Breaker = S['Breaker']
export type Degradation = S['Degradation']
export type PlanEffect = S['PlanEffect']
export type ProtocolChange = S['Change']
export type WorkItem = S['WorkItem']
export type WorkUpdate = S['WorkUpdate']
export type WorkStatus = WorkItem['status']
export type PlanCompat = S['PlanCompat']

// ---------- events and notifications ----------
export type EventFilter = S['EventFilter']
export type DestinationSpec = S['DestinationSpec']
export type DestinationRes = Filled<S['ResourceDestinationSpec']>
export type ChannelSpec = S['ChannelSpec']
export type ChannelRes = Filled<S['ResourceChannelSpec']>
export type NotificationRuleSpec = S['NotificationRuleSpec']
export type NotificationRuleRes = Filled<S['ResourceNotificationRuleSpec']>
export type EventKindInfo = S['KindInfo']
export type EventItem = S['EventDTO']
export type Delivery = S['Delivery']
export type NotificationItem = S['Notification']
export type Severity = 'info' | 'warning' | 'critical'

/** The event envelope carried by every event (EventDTO.envelope). */
export interface EventEnvelope {
  schema_version: number
  id: string
  type: string
  name: string
  occurred_at: string
  severity: string
  correlation_id?: string
  subjects?: Record<string, string>
  data?: Record<string, unknown>
}

// ---------- statistics ----------
export type Stats = S['Stats']
export type StatsGroup = S['Group']
export type StatsLatency = S['Latency']
export type StatsCost = S['Cost']
export type StatsBy = 'client' | 'harness' | 'project' | 'plan' | 'intent' | 'model' | 'account' | 'provider' | 'session' | 'period'

// ---------- backups ----------
// Hand-written: the backup operations are not yet in api/openapi.yaml
// (regenerate with `make gen` and switch these to schema aliases).
export interface BackupFile {
  name: string
  sha256: string
  bytes: number
  schema_version: number
}

export interface BackupManifest {
  format: number
  created_at: string
  server_version: string
  master_key_ids: string[] | null
  include_traces: boolean
  files: BackupFile[] | null
}

export interface Backup {
  name: string
  path: string
  intact: boolean
  problem?: string
  manifest?: BackupManifest
}

export interface BackupCreated {
  path: string
  manifest: BackupManifest
}

export type AttentionItem = S['AttentionItem']
