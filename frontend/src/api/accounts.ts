import { computed, reactive, ref } from 'vue'
import { json, request } from './system'
import type { SubscriptionUsage } from '../community'

export interface User {
  id: number; username: string; display_name: string; role: 'admin' | 'manager' | 'user'; enabled: boolean
  node_limit: number; source_limit: number; subscription_limit: number; created_at: string
  xp: number; points: number; level: number; checkin_on: string; checkin_streak: number; subscription_expires_at: string | null
  title?: string; avatar_version?: number; risk_frozen_until?: string | null
  email?: string; email_verified?: boolean; has_password?: boolean; beacon_enabled?: boolean
}
export interface Quota { nodes: number; sources: number; subscriptions: number }
export interface Limits extends Quota { level: Quota; personal: Quota; boost: Quota }
export interface LoginOptions { email: boolean; github: boolean; linuxdo: boolean; require_email: boolean; email_suffixes: string[] }
export interface AuthResponse { authenticated: boolean; username?: string; user?: User | null; persistent?: boolean; guest_browse?: boolean; registration?: boolean; login_options?: LoginOptions; email_required?: boolean }
export interface Usage { nodes: number; sources: number; subscriptions: number }
export interface Profile { user: User; usage: Usage; limits?: Limits; provider_path: string }
export interface Page<T> { items: T[]; total: number; page: number; page_size: number }
export interface Provider {
  id: number; username: string; display_name: string; source_count: number; node_count: number
  followers: number; followed: boolean; provider_path: string; avatar_version?: number; title?: string
}
// unlimited：来源未设置使用人数上限，不占来源额度
export interface NodeSelection { node_id: number; source_id: number; name?: string; source_name?: string; unlimited?: boolean }
export interface RuleGroup {
  name: string; type: 'select' | 'url-test' | 'fallback' | 'load-balance'; include_all: boolean
  node_ids: number[]; countries: string[]; groups: string[]; interval: number; tolerance: number
  // 在包含全部节点的手动选择组中挂载按国家自动生成的分组
  country_groups?: boolean
}
export interface RuleProfile {
  id: number; name: string; routing_enabled: boolean; dns_enabled: boolean; dns_servers: string[]; rules: string[]
  groups?: RuleGroup[] | null
  official?: boolean; readonly?: boolean
  created_at?: string; updated_at?: string
}
export interface Subscription {
  id: number; name: string; node_count: number; source_count: number; created_at: string; updated_at: string
  url: string; nodes: NodeSelection[]; node_ids: number[]; source_ids: number[]; rule_profile_id?: number | null
  // 跟随整个来源：实时同步来源全部可用节点，不计节点额度
  follow_source_ids?: number[]; follow_details?: FollowSource[]; follow_node_count?: number
  node_details?: (NodeSelection & { type: string; server: string; port: number; available: boolean; demoted?: boolean; my_failed?: boolean; min_level?: number })[]
}
export interface FollowSource {
  source_id: number; name: string; node_count: number; available: boolean; min_level: number; max_users: number
  subscription_usage?: SubscriptionUsage | null
  provider_id: number; provider_name: string; provider_avatar: number; provider_title: string
}
export interface SubscriptionResult { subscription: Subscription; truncated: boolean; selected_count: number }
export const currentUser = ref<User | null>(null)
// 未登录访客：只能浏览节点和提供方，需要登录的操作统一弹出登录框
export const isGuest = ref(false)
export const authOptions = reactive({ guestBrowse: false, registration: false, login: { email: false, github: false, linuxdo: false, require_email: false, email_suffixes: [] } as LoginOptions })
// 需要补齐邮箱的账号：登录后弹出不可关闭的绑定窗口
export const emailRequired = ref(false)
// 我的账号当前分区，OAuth 绑定回跳时切到资料与安全
export const accountTab = ref<'overview' | 'security' | 'points'>('overview')
export const authPrompt = ref<{ mode: 'login' | 'register'; reason?: string } | null>(null)
export function requireLogin(reason?: string, mode: 'login' | 'register' = 'login') {
  if (!isGuest.value) return false
  authPrompt.value = { mode, reason }
  return true
}
export const isAdmin = computed(() => currentUser.value?.role === 'admin')
// 用户管理员只能管理普通用户，没有系统设置权限
export const canManageUsers = computed(() => currentUser.value?.role === 'admin' || currentUser.value?.role === 'manager')
export const roleNames: Record<string, string> = { admin: '管理员', manager: '用户管理员', user: '普通用户' }
export const profile = ref<Profile | null>(null)
export const followed = ref(new Set<number>())
// 实际生效额度：等级默认、管理员设置与临时扩容合成
export const limits = computed<Quota>(() => profile.value?.limits ?? {
  nodes: currentUser.value?.node_limit ?? 200, sources: currentUser.value?.source_limit ?? 3, subscriptions: currentUser.value?.subscription_limit ?? 1,
})
export const selectionNotice = ref('')
export const draftNodes = ref<NodeSelection[]>([])
export const draftSources = ref<{ id: number; name: string; node_count: number; max_users?: number; min_level?: number; subscription_usage?: SubscriptionUsage | null }[]>([])
export const editingSubscription = ref<number | null>(null)
export const draftName = ref('我的订阅')
export const draftRuleProfile = ref<number | null>(0)
export const selectingNodes = ref(false)
export const selectionError = ref('')
export const selectedSources = computed(() => new Set([...draftNodes.value.map(n => n.source_id), ...draftSources.value.map(s => s.id)]))
// 只有设置了使用人数上限的来源才计入来源额度
export const limitedSources = computed(() => new Set([...draftNodes.value.filter(n => !n.unlimited).map(n => n.source_id), ...draftSources.value.filter(s => !!s.max_users).map(s => s.id)]))
// 跟随来源的节点不计入节点额度，只用于预估
export const draftEstimate = computed(() => draftNodes.value.length + draftSources.value.reduce((n, s) => n + s.node_count, 0))
let accountRevision = 0
export function resetDraft() { draftNodes.value = []; draftSources.value = []; editingSubscription.value = null; draftName.value = '我的订阅'; draftRuleProfile.value = 0; selectionError.value = ''; selectionNotice.value = '' }
export function clearAccount() { accountRevision++; currentUser.value = null; profile.value = null; followed.value = new Set(); resetDraft(); selectingNodes.value = false }
export async function loadProfile() {
  const revision = accountRevision
  const value = await request<Profile>('/me')
  if (revision === accountRevision && currentUser.value?.id === value.user.id) { profile.value = value; currentUser.value = value.user }
  return value
}
export function toggleNode(item: NodeSelection) {
  selectionError.value = ''; selectionNotice.value = ''
  if (draftNodes.value.some(n => n.node_id === item.node_id)) { draftNodes.value = draftNodes.value.filter(n => n.node_id !== item.node_id); return }
  if (draftSources.value.some(s => s.id === item.source_id)) { selectionNotice.value = `已跟随整个来源「${item.source_name ?? ''}」，其中的节点无需单独勾选。`; return }
  if (!item.unlimited && !limitedSources.value.has(item.source_id) && limitedSources.value.size >= limits.value.sources) { selectionError.value = '已达到本次选区的来源上限（不限人数的来源不占额度）。'; return }
  if (draftNodes.value.length >= limits.value.nodes) { selectionError.value = '已达到节点上限，请先移除部分节点。'; return }
  draftNodes.value = [...draftNodes.value, item]
}
export function toggleSource(source: { id: number; name: string; node_count: number; max_users?: number }) {
  selectionError.value = ''; selectionNotice.value = ''
  if (draftSources.value.some(s => s.id === source.id)) { draftSources.value = draftSources.value.filter(s => s.id !== source.id); return }
  if (source.max_users && !limitedSources.value.has(source.id) && limitedSources.value.size >= limits.value.sources) { selectionError.value = '已达到本次选区的来源上限（不限人数的来源不占额度）。'; return }
  // 跟随来源已包含其全部节点，移除同源单独勾选的节点
  const duplicate = draftNodes.value.filter(n => n.source_id === source.id).length
  if (duplicate) { draftNodes.value = draftNodes.value.filter(n => n.source_id !== source.id); selectionNotice.value = `已跟随整个来源「${source.name}」，移除了其中 ${duplicate} 个单独勾选的节点。` }
  draftSources.value = [...draftSources.value, { ...source, max_users: source.max_users ?? 0 }]
}
export function editSubscription(item: Subscription) {
  resetDraft(); editingSubscription.value = item.id; draftName.value = item.name; draftRuleProfile.value = item.rule_profile_id ?? null; selectingNodes.value = true
  draftNodes.value = (item.nodes ?? []).map(n => ({ ...n, ...item.node_details?.find(detail => detail.node_id === n.node_id && detail.source_id === n.source_id) }))
  draftSources.value = (item.follow_details ?? []).map(s => ({ id: s.source_id, name: s.name, node_count: s.node_count, max_users: s.max_users, min_level: s.min_level, subscription_usage: s.subscription_usage }))
}
export async function saveDraft(): Promise<SubscriptionResult> {
  const id = editingSubscription.value
  const whole = new Set(draftSources.value.map(s => s.id))
  const payload = { name: draftName.value.trim(), nodes: draftNodes.value.filter(n => !whole.has(n.source_id)).map(({ node_id, source_id }) => ({ node_id, source_id })), source_ids: [], follow_source_ids: draftSources.value.map(s => s.id), rule_profile_id: draftRuleProfile.value }
  return request<SubscriptionResult>(id ? `/subscriptions/${id}` : '/subscriptions', json(id ? 'PUT' : 'POST', payload))
}
export async function toggleFollow(id: number) {
  const revision = accountRevision
  const result = await request<{ followed: boolean }>(`/providers/${id}/follow`, json(followed.value.has(id) ? 'DELETE' : 'POST'))
  if (revision !== accountRevision) return
  const next = new Set(followed.value); if (result.followed) next.add(id); else next.delete(id); followed.value = next
}
export function absoluteURL(value: string) { return new URL(value, location.origin).href }
export async function copyText(value: string) {
  if (navigator.clipboard && window.isSecureContext) { await navigator.clipboard.writeText(value); return }
  const area = document.createElement('textarea'); area.value = value; area.style.position = 'fixed'; area.style.opacity = '0'
  const parent = document.querySelector('dialog[open]') ?? document.body
  const active = document.activeElement as HTMLElement | null
  parent.append(area); area.select(); const ok = document.execCommand('copy'); area.remove(); active?.focus()
  if (!ok) throw new Error('无法自动复制，请选中链接手动复制。')
}

// 没有密码的账号通过第三方验证身份后，登录完成时要求设置密码
const passwordSetupKey = 'nodepool-password-setup'
export function markPasswordSetup() { try { sessionStorage.setItem(passwordSetupKey, '1') } catch { /* 隐私模式下忽略 */ } }
export function passwordSetupPending() { try { return sessionStorage.getItem(passwordSetupKey) === '1' } catch { return false } }
export function clearPasswordSetup() { try { sessionStorage.removeItem(passwordSetupKey) } catch { /* 隐私模式下忽略 */ } }
