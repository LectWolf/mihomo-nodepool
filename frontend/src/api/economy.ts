import { json, request } from './system'
import { currentUser, profile, type Limits, type User } from './accounts'

export interface CommunityRules {
  level_quotas: { nodes: number; sources: number; subscriptions: number }[]
  share_xp_per_user: number; share_xp_source_cap: number; share_xp_daily_cap: number; share_points_percent: number
  share_quality_full: number; share_quality_half: number; unique_bonus_percent: number; unique_share_threshold: number
  milestone_days: number[]; milestone_xp: number[]; achievement_xp: number; probe_xp: number
  makeup_price: number; makeup_monthly: number; leave_price: number; leave_days: number; leave_monthly: number
  boost_days: number; boost_node_price: number; boost_node_amount: number; boost_source_price: number; boost_subscription_price: number; boost_max_active: number
  tip_max: number; titles: { name: string; price: number; color?: string }[]
  risk_enabled: boolean; risk_window_days: number; risk_exposure_multiplier: number; risk_daily_new_nodes: number; risk_daily_new_sources: number
  feedback_enabled: boolean; feedback_visible_days: number
  feedback_reward_xp?: number; feedback_reward_points?: number; feedback_reward_daily?: number; feedback_demote_users?: number
}
export interface Wallet {
  user: User; limits: Limits; boosts: { kind: 'node' | 'source' | 'subscription'; amount: number; expires_at: string }[]
  titles: string[]; makeup_used: number; leave_used: number; missed_days: number; today_share_xp: number
  rules: CommunityRules; achievements: Record<string, boolean>
}
export interface LedgerEntry { id: number; kind: string; xp: number; points: number; note: string; created_at: string }
export interface LedgerPage { items: LedgerEntry[]; total: number; page: number; page_size: number }
export const ledgerKinds: Record<string, string> = {
  checkin: '签到', makeup: '补签', leave: '请假延期', boost_node: '节点扩容', boost_source: '来源扩容', boost_subscription: '订阅扩容',
  title: '兑换称号', tip_out: '打赏', tip_in: '收到打赏', share: '共享奖励', milestone: '稳定共享里程碑', achievement: '成就', probe: '自动续期', admin: '管理员调整', feedback: '反馈被确认',
  legacy_linuxdo: '旧站 LINUX DO 信任等级', legacy_provide: '旧站共享奖励', legacy_debit: '旧站扣除', legacy: '旧站记录',
}
export interface Feedback {
  id: number; target: 'node' | 'source'; node_id: number; source_id: number; node_name: string; source_name: string
  kind: string; comment: string; probe_status: string; probe_latency: number | null; usage_days: number; user_level: number
  reporter: string; reporter_avatar: number; reporter_id?: number; created_at: string; resolved_at?: string | null
  checked_ok_after: boolean; mine: boolean; can_resolve: boolean; can_delete: boolean
}
export interface FeedbackPage { items: Feedback[]; total: number; page: number; page_size: number; visible_days: number }
export const feedbackKinds = {
  node: [{ value: 'unusable', label: '无法使用', tone: 'bad' }, { value: 'slow', label: '速度慢', tone: 'warn' }, { value: 'unstable', label: '不稳定', tone: 'warn' }, { value: 'ok', label: '可正常使用', tone: 'good' }],
  source: [{ value: 'expired', label: '订阅已过期', tone: 'bad' }, { value: 'many_down', label: '大量节点失效', tone: 'bad' }, { value: 'slow', label: '整体较慢', tone: 'warn' }, { value: 'ok', label: '整体可用', tone: 'good' }],
} as const
export function feedbackLabel(target: 'node' | 'source', kind: string) {
  return (feedbackKinds[target] as readonly { value: string; label: string }[]).find(item => item.value === kind)?.label ?? kind
}
export function feedbackTone(target: 'node' | 'source', kind: string) {
  return (feedbackKinds[target] as readonly { value: string; tone: string }[]).find(item => item.value === kind)?.tone ?? 'warn'
}
function applyWallet(w: Wallet) {
  if (currentUser.value?.id === w.user.id) currentUser.value = w.user
  if (profile.value && profile.value.user.id === w.user.id) profile.value = { ...profile.value, user: w.user, limits: w.limits }
  return w
}
export const walletApi = {
  load: () => request<Wallet>('/me/wallet').then(applyWallet),
  makeup: () => request<Wallet>('/me/makeup', json('POST', {})).then(applyWallet),
  leave: () => request<Wallet>('/me/leave', json('POST', {})).then(applyWallet),
  boost: (kind: string) => request<Wallet>('/me/boosts', json('POST', { kind })).then(applyWallet),
  buyTitle: (name: string) => request<Wallet>('/me/titles', json('POST', { name })).then(applyWallet),
  equipTitle: (name: string) => request<Wallet>('/me/title', json('PUT', { name })).then(applyWallet),
  tip: (provider: number, amount: number) => request<Wallet>(`/providers/${provider}/tip`, json('POST', { amount })).then(applyWallet),
  ledger: (page: number, size = 10) => request<LedgerPage>(`/me/ledger?page=${page}&page_size=${size}`),
}
export async function uploadAvatar(blob: Blob) {
  const user = await request<User>('/me/avatar', { method: 'POST', body: blob, headers: { 'Content-Type': blob.type || 'image/png' } })
  if (currentUser.value?.id === user.id) currentUser.value = user
  return user
}
export async function removeAvatar() {
  const user = await request<User>('/me/avatar', json('DELETE'))
  if (currentUser.value?.id === user.id) currentUser.value = user
  return user
}
// 裁剪为居中正方形并缩放到 256 像素，减小上传体积
export async function squareAvatar(file: File): Promise<Blob> {
  if (!/^image\//.test(file.type)) throw new Error('请选择图片文件。')
  const bitmap = await createImageBitmap(file)
  const side = Math.min(bitmap.width, bitmap.height)
  const canvas = document.createElement('canvas'); canvas.width = canvas.height = 256
  const context = canvas.getContext('2d'); if (!context) throw new Error('浏览器无法处理图片。')
  context.drawImage(bitmap, (bitmap.width - side) / 2, (bitmap.height - side) / 2, side, side, 0, 0, 256, 256)
  bitmap.close()
  for (const quality of [0.9, 0.8, 0.65, 0.5]) {
    const blob = await new Promise<Blob | null>(resolve => canvas.toBlob(resolve, 'image/jpeg', quality))
    if (blob && blob.size <= 200 * 1024) return blob
  }
  throw new Error('图片压缩后仍超过 200KB，请换一张图片。')
}
export const feedbackApi = {
  list: (params: Record<string, string | number>) => request<FeedbackPage>(`/feedback?${new URLSearchParams(Object.entries(params).map(([k, v]) => [k, String(v)]))}`),
  submit: (body: { target: 'node' | 'source'; node_id?: number; source_id: number; kind: string; comment: string }) => request<Feedback>('/feedback', json('POST', body)),
  resolve: (id: number, resolved: boolean) => request<{ ok: boolean }>(`/feedback/${id}`, json('PATCH', { resolved })),
  remove: (id: number) => request<{ ok: boolean }>(`/feedback/${id}`, json('DELETE')),
}
