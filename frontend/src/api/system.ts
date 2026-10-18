import { onBeforeUnmount } from 'vue'
import type { FeedbackSummary, NodeInsights, SubscriptionUsage } from '../community'

export interface SystemInfo {
  name: string; version: string; commit: string; build_time: string
  sampled_at?: string; tasks_error?: string
  database: { configured: boolean; connected: boolean }
  data_encryption?: boolean; trusted_proxies?: number
  core: { state: 'ready' | 'error'; version: string }
  tasks: { pending_sources: number; pending_nodes: number; active_sources: number; active_nodes: number }
  maintenance?: Maintenance
}
export interface Maintenance { enabled: boolean; message: string; since?: string | null }
export interface TrafficPoint { at: string; bytes: number }

export interface TaskItem { id: number; name: string; type: string; status: string; lease_until?: string; next_at?: string; last_error?: string }
export interface SystemStatistics {
  overview: {
    sources_total: number; sources_enabled: number; sources_ready: number; sources_error: number
    nodes_total: number; nodes_visible: number; nodes_healthy: number; nodes_unhealthy: number; nodes_unknown: number; nodes_blocked: number
    users_total: number; users_enabled: number; providers_total: number; subscriptions: number; active_seats: number
  }
  tasks: SystemInfo['tasks']; source_tasks: TaskItem[]; node_tasks: TaskItem[]
  sampled_at: string
  runtime: { started_at: string; uptime_seconds: number; heap_bytes: number; goroutines: number; source_body_bytes: number; source_runs: number; source_errors: number; probe_runs: number; probe_errors: number }
  traffic?: { hourly: TrafficPoint[]; daily: TrafficPoint[] }
  usage: { sources_known: number; sources_total: number; upload_bytes: number | null; download_bytes: number | null; total_bytes: number | null; remaining_bytes: number | null; upload_sources: number; download_sources: number; total_sources: number; remaining_sources: number; overflow: boolean; items: { id: number; name: string; enabled: boolean; usage: SubscriptionUsage | null }[] }
}
export interface Source {
  id: number; name: string; kind: 'url' | 'file' | 'links'; enabled: boolean; exclude_pattern: string
  status: 'queued' | 'processing' | 'ready' | 'error'; node_count: number; excluded_count: number
  last_error: string; last_refresh_at: string | null; next_refresh_at: string | null; created_at: string
  owner_id?: number; provider_name?: string; max_users?: number; claimed_users?: number; active_users?: number
  occupied_users?: number; min_level?: number; share_url?: string; subscription_usage?: SubscriptionUsage | null
  // 私有来源：只对提供者和被分享的用户可见
  private?: boolean; share_count?: number; shared_with_me?: boolean
  provider_username?: string; provider_avatar?: number; provider_title?: string; feedback?: FeedbackSummary | null
  // 来源目录补充：健康概况与当前用户可用性
  healthy_count?: number; avg_latency?: number | null; countries?: string[]; unavailable?: Unavailable; seated?: boolean; fresh?: boolean
  takedown?: SourceTakedown | null
}
// 管理员下架记录：下架期间提供方不能自行启用
export interface SourceTakedown { reason: string; reason_label: string; detail: string; at: string; by: string }
// 当前用户无法选用的原因：席位已满、等级不足、被提供方移出
export type Unavailable = '' | 'full' | 'level' | 'kicked' | 'exhausted' | 'expired'
/** 统计窗口内国内用户客户端实测（按用户去重） */
export interface FieldTest { ok: number; fail: number; days?: number; paused?: boolean }
export interface PoolNode {
  id: number; name: string; type: string; server: string; port: number; latency_ms: number | null
  status: 'unknown' | 'healthy' | 'unhealthy'; country: string; country_code: string; exit_ip: string
  region?: string; city?: string
  last_checked_at: string | null; next_check_at: string; last_error: string
  insights?: NodeInsights; server_hidden?: boolean; feedback?: FeedbackSummary | null
  unavailable?: Unavailable; fresh?: boolean; created_at?: string
  field_test?: FieldTest | null
  sources: {
    id: number; name: string; node_name: string; owner_id?: number; provider_name?: string; min_level?: number; private?: boolean; subscription_usage?: SubscriptionUsage | null
    provider_username?: string; provider_avatar?: number; provider_title?: string; max_users?: number; active_users?: number; feedback?: FeedbackSummary | null
    unavailable?: Unavailable; seated?: boolean
  }[]
}
export interface NodePage {
  items: PoolNode[]; total: number; page: number; page_size: number
  stats: { total: number; healthy: number; unhealthy: number; pending: number }
}
export interface Options { types: string[]; countries: { code: string; name: string }[] }
export interface Settings {
  exclude_pattern: string; test_url: string; timeout_ms: number; probe_concurrency: number
  source_concurrency: number; refresh_hours: number; check_hours: number
  subscription_lease_days?: number; history_retention_days?: number; field_test_days?: number; idle_check_days?: number; public_url?: string
  community?: CommunitySettings
  maintenance?: Maintenance
  login?: LoginSettings
}
export interface OAuthSettings { enabled: boolean; client_id: string; client_secret?: string; secret_set?: boolean }
export interface LoginSettings {
  require_email: boolean; email_suffixes: string[]
  smtp: { host: string; port: number; security: 'ssl' | 'starttls' | 'none'; username: string; password?: string; password_set?: boolean; from: string; from_name: string }
  github: OAuthSettings; linuxdo: OAuthSettings
}
export interface CommunitySettings {
  level_quotas: { nodes: number; sources: number; subscriptions: number }[]
  share_xp_per_user: number; share_xp_source_cap: number; share_xp_daily_cap: number; share_points_percent: number
  share_quality_full: number; share_quality_half: number; unique_bonus_percent: number; unique_share_threshold: number
  milestone_xp: number[]; achievement_xp: number; probe_xp: number
  makeup_price: number; makeup_monthly: number; leave_price: number; leave_days: number; leave_monthly: number
  boost_days: number; boost_node_price: number; boost_node_amount: number; boost_source_price: number; boost_subscription_price: number; boost_max_active: number
  tip_max: number; titles: { name: string; price: number; color?: string }[]
  risk_enabled: boolean; risk_window_days: number; risk_exposure_multiplier: number; risk_daily_new_nodes: number; risk_daily_new_sources: number; risk_pulls_per_hour: number; risk_distinct_ips: number
  feedback_enabled: boolean; feedback_daily_limit: number; feedback_visible_days: number
  feedback_reward_xp: number; feedback_reward_points: number; feedback_reward_daily: number; feedback_demote_users: number
  usage_poll_enabled: boolean
  guest_browse: boolean; registration_enabled: boolean
}
export interface Release { version: string; name: string; body: string; published_at: string; url: string }
export interface Releases {
  current_version: string; latest_version: string; update_available: boolean; can_update: boolean
  can_rollback: boolean; need_restart: boolean
  state: 'idle' | 'checking' | 'downloading' | 'installing' | 'restarting' | 'done' | 'error'
  message: string; releases: Release[]
}
export interface NewSource { name: string; kind: Source['kind']; url?: string; content?: string; exclude_pattern?: string; max_users?: number; min_level?: number; private?: boolean }
export class ApiError extends Error {
  readonly status: number
  readonly body: unknown
  constructor(message: string, status = 0, body: unknown = null) { super(message); this.name = 'ApiError'; this.status = status; this.body = body }
}
export const apiEvents = new EventTarget()
const controllers = new Set<AbortController>()
export function abortRequests() { controllers.forEach(controller => controller.abort()) }
export function isCancelled(error: unknown): boolean { return error instanceof Error && error.name === 'AbortError' }
export function errorText(error: unknown): string { return error instanceof Error ? error.message : '操作失败，请稍后重试。' }

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const controller = new AbortController()
  const external = init.signal
  const abort = () => controller.abort()
  if (external?.aborted) controller.abort()
  external?.addEventListener('abort', abort, { once: true })
  controllers.add(controller)
  let timedOut = false
  const timer = window.setTimeout(() => { timedOut = true; controller.abort() }, 20000)
  try {
    const response = await fetch(`/api/v1${path}`, {
      ...init, credentials: 'same-origin', cache: 'no-store', signal: controller.signal,
      headers: { Accept: 'application/json', ...(init.body ? { 'Content-Type': 'application/json' } : {}), ...init.headers },
    })
    const body: unknown = await response.json().catch(() => null)
    if (!response.ok) {
      const detail = body && typeof body === 'object' && 'error' in body && typeof body.error === 'string' ? body.error : `请求失败（${response.status}）`
      if (response.status === 401) apiEvents.dispatchEvent(new Event('unauthorized'))
      if (response.status === 403 && body && typeof body === 'object' && 'email_required' in body) apiEvents.dispatchEvent(new Event('email-required'))
      if (response.status === 503 && body && typeof body === 'object' && 'maintenance' in body) {
        apiEvents.dispatchEvent(new Event('maintenance'))
        throw new ApiError(detail, 503)
      }
      if (response.status === 503) {
        apiEvents.dispatchEvent(new Event('database-unavailable'))
        throw new ApiError(`数据库暂不可用。${detail}`, 503)
      }
      throw new ApiError(detail, response.status, body)
    }
    if (body === null) throw new ApiError('服务返回了无法读取的响应，请重试。')
    return body as T
  } catch (error) {
    if (timedOut) throw new ApiError('请求超时，请检查服务连接后重试。')
    if (isCancelled(error) || error instanceof ApiError) throw error
    throw new ApiError('无法连接服务，请检查网络后重试。')
  } finally {
    clearTimeout(timer)
    external?.removeEventListener('abort', abort)
    controllers.delete(controller)
  }
}
export const json = (method: string, body?: unknown): RequestInit => ({ method, ...(body === undefined ? {} : { body: JSON.stringify(body) }) })
export function useApi() {
  const lifetime = new AbortController()
  onBeforeUnmount(() => lifetime.abort())
  return <T>(path: string, init: RequestInit = {}) => request<T>(path, { ...init, signal: init.signal ?? lifetime.signal })
}
export function formatDate(value?: string | null): string {
  if (!value) return '尚未执行'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}
export function safeReleaseUrl(value: string): string | undefined {
  try { const url = new URL(value); return url.protocol === 'https:' ? url.href : undefined } catch { return undefined }
}
