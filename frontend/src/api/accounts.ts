import { computed, ref } from 'vue'
import { json, request } from './system'

export interface User {
  id: number; username: string; display_name: string; role: 'admin' | 'user'; enabled: boolean
  node_limit: number; source_limit: number; subscription_limit: number; created_at: string
  xp: number; points: number; level: number; checkin_on: string; checkin_streak: number; subscription_expires_at: string | null
}
export interface AuthResponse { authenticated: boolean; username?: string; user?: User | null }
export interface Usage { nodes: number; sources: number; subscriptions: number }
export interface Profile { user: User; usage: Usage; provider_path: string }
export interface Page<T> { items: T[]; total: number; page: number; page_size: number }
export interface Provider {
  id: number; username: string; display_name: string; source_count: number; node_count: number
  followers: number; followed: boolean; provider_path: string
}
export interface NodeSelection { node_id: number; source_id: number; name?: string; source_name?: string }
export interface RuleGroup {
  name: string; type: 'select' | 'url-test' | 'fallback' | 'load-balance'; include_all: boolean
  node_ids: number[]; countries: string[]; groups: string[]; interval: number; tolerance: number
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
  node_details?: (NodeSelection & { type: string; server: string; port: number; available: boolean })[]
}
export interface SubscriptionResult { subscription: Subscription; truncated: boolean; selected_count: number }
export const currentUser = ref<User | null>(null)
export const isAdmin = computed(() => currentUser.value?.role === 'admin')
export const profile = ref<Profile | null>(null)
export const followed = ref(new Set<number>())
export const draftNodes = ref<NodeSelection[]>([])
export const draftSources = ref<{ id: number; name: string; node_count: number }[]>([])
export const editingSubscription = ref<number | null>(null)
export const draftName = ref('我的订阅')
export const draftRuleProfile = ref<number | null>(0)
export const selectingNodes = ref(false)
export const selectionError = ref('')
export const selectedSources = computed(() => new Set([...draftNodes.value.map(n => n.source_id), ...draftSources.value.map(s => s.id)]))
export const draftEstimate = computed(() => draftNodes.value.length + draftSources.value.reduce((n, s) => n + s.node_count, 0))
let accountRevision = 0
export function resetDraft() { draftNodes.value = []; draftSources.value = []; editingSubscription.value = null; draftName.value = '我的订阅'; draftRuleProfile.value = 0; selectionError.value = '' }
export function clearAccount() { accountRevision++; currentUser.value = null; profile.value = null; followed.value = new Set(); resetDraft(); selectingNodes.value = false }
export async function loadProfile() {
  const revision = accountRevision
  const value = await request<Profile>('/me')
  if (revision === accountRevision && currentUser.value?.id === value.user.id) { profile.value = value; currentUser.value = value.user }
  return value
}
export function toggleNode(item: NodeSelection) {
  selectionError.value = ''
  if (draftNodes.value.some(n => n.node_id === item.node_id)) { draftNodes.value = draftNodes.value.filter(n => n.node_id !== item.node_id); return }
  if (!selectedSources.value.has(item.source_id) && selectedSources.value.size >= (currentUser.value?.source_limit ?? 3)) { selectionError.value = '已达到本次选区的来源上限。'; return }
  if (draftNodes.value.length >= (currentUser.value?.node_limit ?? 200)) { selectionError.value = '已达到节点上限，请先移除部分节点。'; return }
  draftNodes.value = [...draftNodes.value, item]
}
export function toggleSource(source: { id: number; name: string; node_count: number }) {
  selectionError.value = ''
  if (draftSources.value.some(s => s.id === source.id)) { draftSources.value = draftSources.value.filter(s => s.id !== source.id); return }
  if (!selectedSources.value.has(source.id) && selectedSources.value.size >= (currentUser.value?.source_limit ?? 3)) { selectionError.value = '已达到本次选区的来源上限。'; return }
  draftSources.value = [...draftSources.value, source]
}
export function editSubscription(item: Subscription) {
  resetDraft(); editingSubscription.value = item.id; draftName.value = item.name; draftRuleProfile.value = item.rule_profile_id ?? null; selectingNodes.value = true
  draftNodes.value = (item.nodes ?? []).map(n => ({ ...n, ...item.node_details?.find(detail => detail.node_id === n.node_id && detail.source_id === n.source_id) }))
}
export async function saveDraft(): Promise<SubscriptionResult> {
  const id = editingSubscription.value
  const payload = { name: draftName.value.trim(), nodes: draftNodes.value.map(({ node_id, source_id }) => ({ node_id, source_id })), source_ids: draftSources.value.map(s => s.id), rule_profile_id: draftRuleProfile.value }
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
