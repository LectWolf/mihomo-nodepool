import { computed, onBeforeUnmount, watch } from 'vue'
import { currentUser, type NodeSelection, type Page, type User } from './api/accounts'
import type { PoolNode } from './api/system'

export const levelThresholds = [0, 50, 200, 1000, 3500, 10000, 30000] as const
export const levelNames = ['初至', '识途', '通行', '常客', '熟路', '向导', '彼岸'] as const
export function levelForXP(xp: number) { return Math.max(0, levelThresholds.filter(threshold => xp >= threshold).length - 1) }
export const levelOptions = levelNames.map((name, value) => ({ value, label: value === 0 ? 'Lv0 不限' : `Lv${value} ${name}` }))
export interface CommunityFields {
  xp: number; points: number; level: number; checkin_on: string; checkin_streak: number
  subscription_expires_at?: string | null
}
export type CommunityUser = User & CommunityFields
export function userCommunity(user: User | null): CommunityFields {
  const value = user as (User & Partial<CommunityFields>) | null
  const xp = value?.xp ?? 0
  return {
    xp, points: value?.points ?? 0,
    level: Math.max(0, Math.min(6, value?.level ?? levelForXP(xp))),
    checkin_on: value?.checkin_on ?? '', checkin_streak: value?.checkin_streak ?? 0,
    subscription_expires_at: value?.subscription_expires_at,
  }
}
export const communityUser = computed(() => userCommunity(currentUser.value))
export function levelLabel(level: number) { return `Lv${level} ${levelNames[level] ?? levelNames[0]}` }
export function beijingDate(date = new Date()) {
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(date)
}
export interface CheckinResult {
  user: CommunityUser; awarded: boolean; xp_added: number; points_added: number; needs_confirmation: boolean
  skipped: { source_id: number; name: string; reason: string }[]
}
export interface LevelDistribution { total: number; counts: number[]; thresholds: number[]; names: string[] }
export interface SubscriptionUsage {
  upload_bytes?: number | null; download_bytes?: number | null; total_bytes?: number | null; remaining_bytes?: number | null
  expires_at?: string | null; fetched_at?: string | null
  status: 'unknown' | 'normal' | 'low' | 'exhausted' | 'expired' | 'invalid'
}
export interface NodeInsights {
  v2ray_compatible: boolean; v2ray_reason: string; cipher?: string; transport?: string
  tls: 'enabled' | 'disabled' | 'not_applicable' | 'unknown'
  warnings: { code: string; severity: string; message: string }[]
}
export type DetailedSelection = NodeSelection & {
  type?: string; server?: string; port?: number; available?: boolean; insights?: NodeInsights
  min_level?: number; subscription_usage?: SubscriptionUsage | null
}
export function selectionFor(node: PoolNode, origin: PoolNode['sources'][number]): DetailedSelection {
  return {
    node_id: node.id, source_id: origin.id, source_name: origin.name, name: node.name,
    type: node.type, server: node.server, port: node.port, insights: node.insights,
    min_level: origin.min_level, subscription_usage: origin.subscription_usage,
  }
}
export interface Subscriber {
  user_id: number; username: string; display_name: string; level: number; xp: number; enabled: boolean
  state: 'active' | 'expired' | 'released' | 'kicked'; claimed_at: string; expires_at: string
  last_pull_at?: string | null; last_renewed_at?: string | null; renew_method: string; ended_at?: string | null
  node_count: number; subscription_count: number
}
export interface HistoryItem { source_id: number; source_name: string; action: string; created_at: string }
export type RetainedPage<T> = Page<T> & { history_retention_days: number }
export function historyAction(action: string) {
  const labels: Record<string, string> = { claimed: '领取来源', activated: '激活来源', renewed: '续期', released: '释放席位', expired: '租期到期', kicked: '被移除并禁用', unblocked: '解除禁用', checkin: '网页签到', probe: '探测续期' }
  return labels[action] ?? action
}
export function formatBytes(value: number) {
  if (!Number.isFinite(value)) return '未知'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  const index = Math.min(units.length - 1, Math.floor(Math.log(Math.max(1, Math.abs(value))) / Math.log(1024)))
  return `${(value / 1024 ** index).toLocaleString('zh-CN', { maximumFractionDigits: index ? 2 : 0 })} ${units[index]}`
}
// A logout, account switch, pause or unmount invalidates every pending local response.
export function useAccountScope(paused: () => boolean = () => false) {
  let revision = 0
  watch([() => currentUser.value?.id, paused], () => { revision++ }, { flush: 'sync' })
  onBeforeUnmount(() => { revision++ })
  return { ticket: () => revision, current: (ticket: number) => ticket === revision && !!currentUser.value && !paused() }
}
