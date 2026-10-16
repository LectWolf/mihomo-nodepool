import { onBeforeUnmount } from 'vue'
import type { NodeInsights, SubscriptionUsage } from '../community'

export interface SystemInfo {
  name: string; version: string; commit: string; build_time: string
  database: { configured: boolean; connected: boolean }
  core: { state: 'ready' | 'error'; version: string }
  tasks: { pending_sources: number; pending_nodes: number; active_sources: number; active_nodes: number }
}
export interface Source {
  id: number; name: string; kind: 'url' | 'file' | 'links'; enabled: boolean; exclude_pattern: string
  status: 'queued' | 'processing' | 'ready' | 'error'; node_count: number; excluded_count: number
  last_error: string; last_refresh_at: string | null; next_refresh_at: string | null; created_at: string
  owner_id?: number; provider_name?: string; max_users?: number; claimed_users?: number; active_users?: number
  occupied_users?: number; min_level?: number; share_url?: string; subscription_usage?: SubscriptionUsage | null
}
export interface PoolNode {
  id: number; name: string; type: string; server: string; port: number; latency_ms: number | null
  status: 'unknown' | 'healthy' | 'unhealthy'; country: string; country_code: string; exit_ip: string
  region?: string; city?: string
  last_checked_at: string | null; next_check_at: string; last_error: string
  insights?: NodeInsights
  sources: { id: number; name: string; node_name: string; owner_id?: number; provider_name?: string; min_level?: number; subscription_usage?: SubscriptionUsage | null }[]
}
export interface NodePage {
  items: PoolNode[]; total: number; page: number; page_size: number
  stats: { total: number; healthy: number; unhealthy: number; pending: number }
}
export interface Options { types: string[]; countries: { code: string; name: string }[] }
export interface Settings {
  exclude_pattern: string; test_url: string; timeout_ms: number; probe_concurrency: number
  source_concurrency: number; refresh_hours: number; check_hours: number
  subscription_lease_days?: number; history_retention_days?: number; public_url?: string
}
export interface Release { version: string; name: string; body: string; published_at: string; url: string }
export interface Releases {
  current_version: string; latest_version: string; update_available: boolean; can_update: boolean
  can_rollback: boolean; need_restart: boolean
  state: 'idle' | 'checking' | 'downloading' | 'installing' | 'restarting' | 'done' | 'error'
  message: string; releases: Release[]
}
export interface NewSource { name: string; kind: Source['kind']; url?: string; content?: string; exclude_pattern?: string; max_users?: number; min_level?: number }
export class ApiError extends Error {
  readonly status: number
  constructor(message: string, status = 0) { super(message); this.name = 'ApiError'; this.status = status }
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
      if (response.status === 503) {
        apiEvents.dispatchEvent(new Event('database-unavailable'))
        throw new ApiError(`数据库暂不可用。${detail}`, 503)
      }
      throw new ApiError(detail, response.status)
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
