import { customRef, reactive } from 'vue'

export type ToastTone = 'success' | 'info' | 'warning' | 'error'
export interface ToastAction { label: string; run: () => void }
export interface Toast { id: number; message: string; tone: ToastTone; action?: ToastAction }
export const toasts = reactive<Toast[]>([])
let seq = 0

export function toast(message: string, tone: ToastTone = 'success', options: { ms?: number; action?: ToastAction } = {}) {
  const ms = options.ms ?? (tone === 'error' || options.action ? 6000 : 3600)
  if (!message) return
  // 相同内容只保留一条，避免轮询或重复点击刷屏
  const same = toasts.findIndex(item => item.message === message)
  if (same >= 0) toasts.splice(same, 1)
  const id = ++seq
  toasts.push({ id, message, tone, action: options.action })
  if (toasts.length > 4) toasts.shift()
  window.setTimeout(() => dismissToast(id), ms)
}
export function dismissToast(id: number) {
  const index = toasts.findIndex(item => item.id === id)
  if (index >= 0) toasts.splice(index, 1)
}
// 赋值即弹出提示，读取恒为空字符串，用于替换原来的成功提示条
export function toastRef(tone: ToastTone = 'success') {
  return customRef<string>(() => ({ get: () => '', set: value => { if (value) toast(value, tone) } }))
}
const raisers = new Set<() => void>()
// 新弹窗进入浏览器顶层后调用，让 toast 重新显示在最上层
export function raiseToasts() { for (const fn of raisers) fn() }
export function onRaiseToasts(fn: () => void) { raisers.add(fn); return () => { raisers.delete(fn) } }
