import { computed, onBeforeUnmount, watch } from 'vue'
import { currentUser, isGuest, type NodeSelection, type Page, type User } from './api/accounts'
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
// 当前等级进度：返回距下一级的百分比，满级为 100
export function levelProgress(xp: number) {
  const level = levelForXP(xp), floor = levelThresholds[level], next = levelThresholds[level + 1]
  return { level, next: next ?? null, percent: next == null ? 100 : Math.min(100, Math.max(0, (xp - floor) / (next - floor) * 100)) }
}
export function levelLabel(level: number) { return `Lv${level} ${levelNames[level] ?? levelNames[0]}` }
export function beijingDate(date = new Date()) {
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(date)
}
export interface CheckinResult {
  user: CommunityUser; awarded: boolean; xp_added: number; points_added: number; needs_confirmation: boolean
  skipped: { source_id: number; name: string; reason: string }[]
}
export interface LevelDistribution { total: number; counts: number[]; thresholds: number[]; names: string[]; quotas?: { nodes: number; sources: number; subscriptions: number }[] }
export interface FeedbackSummary { total: number; bad: number; ok: number; kinds: Record<string, number>; latest_at?: string | null }
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
    min_level: origin.min_level, subscription_usage: origin.subscription_usage, unlimited: !origin.max_users,
  }
}
export interface Subscriber {
  user_id: number; username: string; display_name: string; level: number; xp: number; enabled: boolean
  state: 'active' | 'expired' | 'released' | 'kicked'; claimed_at: string; expires_at: string
  last_pull_at?: string | null; last_renewed_at?: string | null; renew_method: string; ended_at?: string | null
  node_count: number; subscription_count: number; avatar_version?: number; title?: string; risk_flags?: string[]
}
export interface HistoryItem { source_id: number; source_name: string; action: string; created_at: string }
export type RetainedPage<T> = Page<T> & { history_retention_days: number }
export function historyAction(action: string) {
  const labels: Record<string, string> = { claimed: '领取来源', activated: '激活来源', renewed: '续期', released: '释放席位', expired: '租期到期', removed: '被提供方踢出', kicked: '被移除并禁用', unblocked: '解除禁用', checkin: '网页签到', probe: '探测续期', leave: '请假延期' }
  return labels[action] ?? action
}
export function formatBytes(value: number) {
  if (!Number.isFinite(value)) return '未知'
  // 与机场面板一致：按 1024 进位，单位写作 GB
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const index = Math.min(units.length - 1, Math.floor(Math.log(Math.max(1, Math.abs(value))) / Math.log(1024)))
  return `${(value / 1024 ** index).toLocaleString('zh-CN', { maximumFractionDigits: index ? 2 : 0 })} ${units[index]}`
}
// A logout, account switch, pause or unmount invalidates every pending local response.
export function useAccountScope(paused: () => boolean = () => false) {
  let revision = 0
  watch([() => currentUser.value?.id, paused], () => { revision++ }, { flush: 'sync' })
  onBeforeUnmount(() => { revision++ })
  return { ticket: () => revision, current: (ticket: number) => ticket === revision && (!!currentUser.value || isGuest.value) && !paused() }
}
export function timeAgo(value?: string | null, now = Date.now()) {
  if (!value) return ''
  const diff = Math.max(0, now - new Date(value).getTime())
  if (!Number.isFinite(diff)) return ''
  const minute = 60000, hour = 60 * minute, day = 24 * hour
  if (diff < minute) return '刚刚'
  if (diff < hour) return `${Math.floor(diff / minute)} 分钟前`
  if (diff < day) return `${Math.floor(diff / hour)} 小时前`
  if (diff < 30 * day) return `${Math.floor(diff / day)} 天前`
  return new Date(value).toLocaleDateString('zh-CN')
}
export function daysUntil(value?: string | null, now = Date.now()) {
  if (!value) return null
  const time = new Date(value).getTime()
  return Number.isFinite(time) ? Math.ceil((time - now) / 86400000) : null
}

// 不可选原因的简短标签
export function blockLabel(reason?: string, minLevel?: number) {
  if (reason === 'full') return '席位已满'
  if (reason === 'level') return `需 Lv${minLevel ?? 0}`
  if (reason === 'kicked') return '已被移出'
  if (reason === 'exhausted') return '流量耗尽'
  if (reason === 'expired') return '套餐到期'
  return ''
}
export function blockHint(reason?: string, minLevel?: number) {
  if (reason === 'full') return '该来源的使用人数已满，等有人退出后才能加入。'
  if (reason === 'level') return `该来源需要 Lv${minLevel ?? 0} 才能使用，签到和共享可以升级。`
  if (reason === 'kicked') return '你已被该来源的提供方移出，无法再次加入。'
  if (reason === 'exhausted') return '该来源的套餐流量已用完，提供方续费或流量重置后恢复。'
  if (reason === 'expired') return '该来源的套餐已到期，提供方续费后恢复。'
  return ''
}
