import { onBeforeUnmount, ref } from 'vue'
import { json, request } from './system'
import type { User } from './accounts'

export type CodePurpose = 'register' | 'login' | 'reset'
export interface Identity { provider: 'github' | 'linuxdo'; login: string; created_at: string }
export const providerNames: Record<string, string> = { github: 'GitHub', linuxdo: 'LINUX DO' }

export const identityApi = {
  sendCode: (email: string, purpose: CodePurpose) => request<{ ok: boolean; cooldown: number }>('/auth/email/code', json('POST', { email, purpose })),
  sendBindCode: (email: string) => request<{ ok: boolean; cooldown: number }>('/me/email/code', json('POST', { email })),
  bindEmail: (email: string, code: string) => request<User>('/me/email', json('PUT', { email, code })),
  identities: () => request<{ items: Identity[]; providers: Record<string, boolean> }>('/me/identities'),
  unlink: (provider: string) => request<{ ok: boolean }>(`/me/identities/${provider}`, json('DELETE')),
  smtpTest: (to: string) => request<{ ok: boolean }>('/settings/smtp-test', json('POST', { to })),
}

// 第三方登录需要整页跳转；remember 沿用“保持登录”的偏好
export function oauthHref(provider: string, mode: 'login' | 'bind', remember = false) {
  const q = new URLSearchParams({ mode })
  if (remember) q.set('remember', '1')
  return `/api/v1/auth/oauth/${provider}/start?${q}`
}

export function emailSuffixAllowed(email: string, suffixes: string[]) {
  const at = email.lastIndexOf('@')
  if (at < 1 || !suffixes.length) return at > 0
  const domain = email.slice(at + 1).toLowerCase()
  return suffixes.some(s => domain === s || domain.endsWith(`.${s}`))
}

// 验证码倒计时
export function useCountdown() {
  const left = ref(0)
  let timer = 0
  function start(seconds = 60) {
    left.value = seconds
    window.clearInterval(timer)
    timer = window.setInterval(() => { left.value = Math.max(0, left.value - 1); if (!left.value) window.clearInterval(timer) }, 1000)
  }
  onBeforeUnmount(() => window.clearInterval(timer))
  return { left, start }
}
