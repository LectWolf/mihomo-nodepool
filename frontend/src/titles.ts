import { ref } from 'vue'
import { request } from './api/system'

// 称号颜色：管理员在运行设置中为每个称号选择
export const titleColors = ['amber', 'blue', 'green', 'violet', 'red', 'pink', 'cyan', 'slate', 'gold'] as const
export const titleColorNames: Record<string, string> = { amber: '琥珀', blue: '天蓝', green: '翠绿', violet: '紫罗兰', red: '赤红', pink: '樱粉', cyan: '青碧', slate: '石墨', gold: '流金' }
const colors = ref<Record<string, string>>({})
let loading: Promise<void> | null = null
export function loadTitles(force = false) {
  if (loading && !force) return loading
  loading = request<{ items: { name: string; color: string }[] }>('/titles')
    .then(v => { colors.value = Object.fromEntries((v.items ?? []).map(t => [t.name, t.color || 'amber'])) })
    .catch(() => { loading = null })
  return loading
}
export function titleColor(name?: string | null) { return (name && colors.value[name]) || 'amber' }
