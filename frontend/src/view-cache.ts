import { currentUser, isGuest } from './api/accounts'

// 页面数据的会话级缓存：刷新后先显示上次结果，再后台更新；按账号隔离，关闭标签页即失效
const prefix = 'np-cache:v1:'
const maxAge = 30 * 60_000
const maxSize = 400_000

function key(path: string) {
  const owner = currentUser.value?.id ? `u${currentUser.value.id}` : isGuest.value ? 'guest' : ''
  return owner ? `${prefix}${owner}:${path}` : ''
}

export function cached<T>(path: string): T | undefined {
  const k = key(path)
  if (!k) return undefined
  try {
    const raw = sessionStorage.getItem(k)
    if (!raw) return undefined
    const entry = JSON.parse(raw) as { at: number; data: T }
    if (Date.now() - entry.at > maxAge) { sessionStorage.removeItem(k); return undefined }
    return entry.data
  } catch { return undefined }
}

export function remember(path: string, data: unknown) {
  const k = key(path)
  if (!k) return
  try {
    const raw = JSON.stringify({ at: Date.now(), data })
    if (raw.length > maxSize) { sessionStorage.removeItem(k); return }
    sessionStorage.setItem(k, raw)
  } catch { clearCache() }
}

export function clearCache() {
  try {
    for (let i = sessionStorage.length - 1; i >= 0; i--) {
      const k = sessionStorage.key(i)
      if (k?.startsWith(prefix)) sessionStorage.removeItem(k)
    }
  } catch { /* 隐私模式下忽略 */ }
}
