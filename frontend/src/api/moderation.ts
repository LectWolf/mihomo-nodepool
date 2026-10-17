import { computed } from 'vue'
import { json, request, type Source } from './system'
import { currentUser, type User } from './accounts'
import type { Identity } from './identity'

export interface TakedownReason { key: string; label: string }
export interface ModerationEvent { id: number; action: 'takedown' | 'restore'; reason: string; reason_label: string; detail: string; operator: string; created_at: string }
export interface ModerationPage { items: Source[]; total: number; takedowns: number; page: number; page_size: number; reasons: TakedownReason[] }
export interface UserStats {
  sources: number; sources_active: number; sources_taken_down: number; provided_nodes: number; subscribers: number; followers: number; following: number
  subscriptions: number; seats: number; pulls_7d: number; pull_ips_7d: number; last_pull_at: string | null; sessions: number; last_login_at: string | null
  feedback: number; points_earned_30d: number; points_spent_30d: number; xp_earned_30d: number
}
export interface UserDetail { user: User; identities: Identity[]; stats: UserStats; sources: Source[] }

// 与后端一致的默认原因，接口返回前先用于渲染
export const takedownReasons: TakedownReason[] = [
  { key: 'owner_request', label: '订阅链接所有方要求下架' },
  { key: 'invalid', label: '节点长期失效或质量过差' },
  { key: 'abuse', label: '违规内容或滥用' },
  { key: 'duplicate', label: '重复或盗用他人来源' },
  { key: 'risk', label: '存在安全风险' },
  { key: 'other', label: '其他原因' },
]

export const isStaff = computed(() => currentUser.value?.role === 'admin' || currentUser.value?.role === 'manager')

export const moderationApi = {
  list: (params: URLSearchParams) => request<ModerationPage>(`/moderation/sources?${params}`),
  takedown: (id: number, reason: string, detail: string, notify: boolean) => request<{ source: Source; notified: boolean }>(`/sources/${id}/takedown`, json('POST', { reason, detail, notify })),
  restore: (id: number, notify: boolean) => request<{ source: Source; notified: boolean }>(`/sources/${id}/restore`, json('POST', { notify })),
  history: (id: number) => request<{ items: ModerationEvent[] }>(`/sources/${id}/moderation`),
  userDetail: (id: number) => request<UserDetail>(`/users/${id}/detail`),
  unlinkIdentity: (id: number, provider: string) => request<{ ok: boolean }>(`/users/${id}/identities/${provider}`, json('DELETE')),
}
