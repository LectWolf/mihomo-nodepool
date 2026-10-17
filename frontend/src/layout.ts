import { computed, ref, watchEffect } from 'vue'

// 界面布局：新版工作台或经典版（顶部导航、按来源浏览），按浏览器记忆
export type Layout = 'modern' | 'classic'
const key = 'nodepool-layout'
function read(): Layout {
  try { return localStorage.getItem(key) === 'classic' ? 'classic' : 'modern' } catch { return 'modern' }
}
const current = ref<Layout>(read())
export const layout = computed(() => current.value)
export const classic = computed(() => current.value === 'classic')
export function setLayout(value: Layout) {
  current.value = value
  try { localStorage.setItem(key, value) } catch { /* 存储不可用时仅本页生效 */ }
}
const stop = watchEffect(() => { document.documentElement.dataset.layout = current.value })
function storageChanged(event: StorageEvent) { if (event.key === key || event.key === null) current.value = read() }
window.addEventListener('storage', storageChanged)
if (import.meta.hot) import.meta.hot.dispose(() => { stop(); window.removeEventListener('storage', storageChanged) })
