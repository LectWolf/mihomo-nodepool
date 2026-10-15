<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi, type NewSource, type Source } from '../api/system'
import Modal from './Modal.vue'
import { route, routeParams, setRouteQuery } from '../router'
const props = defineProps<{ items: Source[]; loading: boolean; error: string }>()
const emit = defineEmits<{ reload: []; updated: [source: Source]; removed: [id: number] }>()
const api = useApi()
const search = ref(routeParams('sources').get('q') ?? '')
watch(route, value => { if (value.section === 'sources') search.value = routeParams('sources').get('q') ?? '' }, { flush: 'sync' })
watch(search, value => {
  if (route.value.section !== 'sources') return
  const params = new URLSearchParams()
  if (value) params.set('q', value)
  setRouteQuery('sources', params, true)
})
const visibleSources = computed(() => props.items.filter(source => source.name.toLowerCase().includes(search.value.toLowerCase())))
const enabledCount = computed(() => props.items.filter(source => source.enabled).length)
const pendingCount = computed(() => props.items.filter(source => ['queued', 'processing'].includes(source.status)).length)
const failedCount = computed(() => props.items.filter(source => source.status === 'error').length)
const totalsKnown = computed(() => !props.error || props.items.length > 0)
const statusLabels = { queued: '等待处理', processing: '处理中', ready: '已就绪', error: '处理失败' }
const kindLabels = { url: '订阅链接', file: '本地文件', links: '节点链接' }
const importOpen = ref(false)
const importKind = ref<Source['kind']>('url')
const importName = ref('')
const importPattern = ref('')
const urlText = ref('')
const linksText = ref('')
const files = ref<File[]>([])
const importBusy = ref(false)
const importError = ref('')
interface ImportResult { key: number; name: string; status: 'submitting' | Source['status']; error: string; id?: number }
const results = ref<ImportResult[]>([])
const resultsExpanded = ref(true)
const finished = computed(() => results.value.filter(result => result.status === 'ready').length)
const failed = computed(() => results.value.filter(result => result.status === 'error').length)
const awaiting = computed(() => results.value.filter(result => !['ready', 'error'].includes(result.status)).length)
const canImport = computed(() => importKind.value === 'url' ? !!urlText.value.trim() : importKind.value === 'file' ? files.value.length > 0 : !!linksText.value.trim())
const busyIds = reactive(new Set<number>())
const rowErrors = reactive<Record<number, string>>({})
const rowMessages = reactive<Record<number, string>>({})
const editing = ref<Source | null>(null)
const editForm = reactive({ name: '', exclude_pattern: '', url: '', enabled: true })
const editBusy = ref(false)
const editError = ref('')
const deleting = ref<Source | null>(null)
const deleteBusy = ref(false)
const deleteError = ref('')
let active = true
onBeforeUnmount(() => { active = false })
watch(() => props.items, items => {
  for (const result of results.value) {
    const source = items.find(item => item.id === result.id)
    if (source) { result.status = source.status; result.error = source.last_error || ''; result.name = source.name }
  }
}, { deep: true })
function selectFiles(event: Event) {
  const input = event.target as HTMLInputElement
  if (input.files) files.value.push(...Array.from(input.files))
  input.value = ''
}
function validUrl(value: string) {
  try { return ['http:', 'https:'].includes(new URL(value).protocol) } catch { return false }
}
async function startImport() {
  if (!canImport.value || importBusy.value) return
  importBusy.value = true; importError.value = ''; resultsExpanded.value = true
  const kind = importKind.value
  const name = importName.value.trim()
  const pattern = importPattern.value
  const rows = kind === 'url' ? urlText.value.split(/\r?\n/).map(value => value.trim()).filter(Boolean) : kind === 'file' ? files.value.map(file => file.name) : ['粘贴的节点链接']
  const selectedFiles = [...files.value]
  const pasted = linksText.value.trim()
  results.value = rows.map((value, index) => ({ key: index, name: name ? `${name}${rows.length > 1 ? ` · ${index + 1}` : ''}` : kind === 'url' ? `订阅 ${index + 1}` : value, status: 'submitting', error: '' }))
  let cursor = 0
  async function worker() {
    while (active && cursor < rows.length) {
      const index = cursor++
      const result = results.value[index]
      const value = rows[index]
      if (!result || value === undefined) continue
      try {
        const payload: NewSource = { name: result.name, kind, exclude_pattern: pattern }
        if (kind === 'url') {
          if (!validUrl(value)) throw new Error(`第 ${index + 1} 行不是有效的 HTTP / HTTPS 订阅地址。`)
          payload.url = value
          if (!name) { result.name = `订阅 · ${new URL(value).hostname}`; payload.name = result.name }
        } else {
          const file = selectedFiles[index]
          payload.content = kind === 'file' && file ? await file.text() : pasted
          if (!payload.content.trim()) throw new Error('内容为空，请选择包含节点的文件或粘贴节点链接。')
        }
        const source = await api<Source>('/sources', json('POST', payload))
        result.id = source.id; result.name = source.name; result.status = source.status; result.error = source.last_error || ''
        emit('reload')
      } catch (error) {
        if (isCancelled(error)) return
        result.status = 'error'; result.error = errorText(error)
      }
    }
  }
  try {
    await Promise.all(Array.from({ length: Math.min(3, rows.length) }, () => worker()))
    if (active) { emit('reload'); urlText.value = ''; linksText.value = ''; files.value = [] }
  } finally { importBusy.value = false }
}
function openEdit(source: Source) {
  editing.value = source
  Object.assign(editForm, { name: source.name, exclude_pattern: source.exclude_pattern, url: '', enabled: source.enabled })
  editError.value = ''
}
async function saveEdit() {
  const source = editing.value
  if (!source || editBusy.value || !editForm.name.trim()) return
  editError.value = ''
  if (source.kind === 'url' && editForm.url.trim() && !validUrl(editForm.url.trim())) { editError.value = '请输入有效的 HTTP / HTTPS 订阅地址，留空则保留原地址。'; return }
  editBusy.value = true
  try {
    const updated = await api<Source>(`/sources/${source.id}`, json('PATCH', { name: editForm.name.trim(), enabled: editForm.enabled, exclude_pattern: editForm.exclude_pattern, ...(source.kind === 'url' && editForm.url.trim() ? { url: editForm.url.trim() } : {}) }))
    emit('updated', updated); emit('reload'); editing.value = null
  } catch (error) { if (!isCancelled(error)) editError.value = errorText(error) }
  finally { editBusy.value = false }
}
async function toggle(source: Source) {
  if (busyIds.has(source.id)) return
  busyIds.add(source.id); rowErrors[source.id] = ''; rowMessages[source.id] = ''
  try {
    const updated = await api<Source>(`/sources/${source.id}`, json('PATCH', { enabled: !source.enabled }))
    emit('updated', updated); emit('reload')
  } catch (error) { if (!isCancelled(error)) rowErrors[source.id] = errorText(error) }
  finally { busyIds.delete(source.id) }
}
async function refresh(source: Source) {
  if (busyIds.has(source.id)) return
  busyIds.add(source.id); rowErrors[source.id] = ''; rowMessages[source.id] = ''
  try {
    await api(`/sources/${source.id}/refresh`, json('POST'))
    emit('updated', { ...source, status: 'queued', last_error: '' })
    rowMessages[source.id] = source.kind === 'url' ? '已加入刷新队列。' : '已加入重新解析队列。'
    emit('reload')
  } catch (error) { if (!isCancelled(error)) rowErrors[source.id] = errorText(error) }
  finally { busyIds.delete(source.id) }
}
function confirmDelete(source: Source) { deleting.value = source; deleteError.value = '' }
async function removeSource() {
  if (!deleting.value || deleteBusy.value) return
  deleteBusy.value = true
  try {
    await api(`/sources/${deleting.value.id}`, json('DELETE'))
    emit('removed', deleting.value.id); emit('reload'); deleting.value = null
  } catch (error) { if (!isCancelled(error)) deleteError.value = errorText(error) }
  finally { deleteBusy.value = false }
}
</script>

<template>
  <section>
    <div class="page-heading"><div><p class="eyebrow">SOURCE LIBRARY</p><h1>来源管理</h1><p class="muted">统一整理订阅与节点链接，保留每个节点的来处。</p></div><button class="primary" :disabled="importBusy" @click="importOpen = true">＋ 添加来源</button></div>
    <div class="source-summary"><div><strong>{{ totalsKnown ? items.length : '—' }}</strong><span>全部来源</span></div><div><strong>{{ totalsKnown ? enabledCount : '—' }}</strong><span>已启用</span></div><div><strong>{{ totalsKnown ? pendingCount : '—' }}</strong><span>等待 / 处理中</span></div><div><strong :class="{ 'danger-text': failedCount > 0 }">{{ totalsKnown ? failedCount : '—' }}</strong><span>处理失败</span></div><p><span class="dot"></span>每 6 小时按来源加入时间错峰刷新</p></div>
    <div v-if="results.length" class="panel import-results" aria-live="polite"><div class="panel-heading"><div><h2>本次导入进度</h2><p class="small muted">{{ finished }} 个已就绪 · {{ awaiting }} 个处理中 · <span :class="{ 'danger-text': failed > 0 }">{{ failed }} 个失败</span></p></div><button @click="resultsExpanded = !resultsExpanded">{{ resultsExpanded ? '收起' : '展开' }}</button></div><p v-if="failed" class="notice warning">{{ finished || awaiting ? '部分来源导入失败，其余来源继续处理。' : '本次来源未能导入。' }}请查看下方原因，可修正后重新添加或逐个重试。</p><ul v-if="resultsExpanded" class="result-list"><li v-for="result in results" :key="result.key"><span><strong>{{ result.name }}</strong><small v-if="result.error" class="danger-text">{{ result.error }}</small></span><span :class="['badge', result.status]">{{ result.status === 'submitting' ? '正在提交' : statusLabels[result.status] }}</span></li></ul></div>
    <div class="panel"><div class="panel-heading"><div><h2>来源列表 <span class="count">{{ items.length }}</span></h2><p class="small muted">刷新失败会保留上次成功内容，重复节点自动合并。</p></div><label class="search-field"><span class="sr-only">搜索来源名称</span><span>⌕</span><input v-model="search" type="search" placeholder="搜索来源名称" /></label></div>
      <div v-if="error" class="notice error" role="alert">{{ error }}<span v-if="items.length">当前保留上次读取结果。</span><button :disabled="loading" @click="emit('reload')">重试</button></div>
      <div v-if="loading && !items.length" class="empty-state" role="status"><span class="spinner"></span><h3>正在读取来源</h3></div>
      <div v-else-if="!visibleSources.length && !error" class="empty-state"><span class="empty-icon">▤</span><h3>{{ search ? '没有匹配的来源' : '从第一个来源开始' }}</h3><p>{{ search ? '尝试其他名称。' : '支持批量订阅地址、多个本地文件和节点分享链接。' }}</p><button v-if="!search" class="primary" @click="importOpen = true">添加来源</button></div>
      <div v-else class="source-list" :aria-busy="loading"><article v-for="source in visibleSources" :key="source.id" :class="['source-card', { disabled: !source.enabled }]"><div class="source-card-head"><span class="source-icon" aria-hidden="true">{{ source.kind === 'url' ? '↗' : source.kind === 'file' ? '▤' : '⌁' }}</span><div class="source-title"><h3>{{ source.name }}</h3><span class="small muted">{{ kindLabels[source.kind] }} · 加入于 {{ formatDate(source.created_at) }}</span></div><span :class="['badge', source.status]">{{ source.enabled ? statusLabels[source.status] : '已停用' }}</span><button :class="['switch', { on: source.enabled }]" role="switch" :aria-checked="source.enabled" :aria-label="`${source.enabled ? '停用' : '启用'} ${source.name}`" :disabled="busyIds.has(source.id) || editBusy || deleteBusy" @click="toggle(source)"><span></span></button></div><div class="source-card-body"><div class="source-numbers"><span><strong>{{ source.node_count }}</strong> 个节点</span><span><strong>{{ source.excluded_count }}</strong> 个已排除</span></div><div class="source-schedule"><span>最近刷新 <b>{{ formatDate(source.last_refresh_at) }}</b></span><span>下次刷新 <b>{{ source.enabled ? formatDate(source.next_refresh_at) : '已暂停' }}</b></span></div><div class="source-actions"><button class="compact" :disabled="busyIds.has(source.id) || !source.enabled || source.status === 'queued' || source.status === 'processing'" @click="refresh(source)">{{ source.kind === 'url' ? '↻ 刷新' : '↻ 重新解析' }}</button><button class="compact" :disabled="busyIds.has(source.id)" @click="openEdit(source)">编辑</button><button class="compact danger" :disabled="busyIds.has(source.id)" @click="confirmDelete(source)">删除</button></div></div><p v-if="source.exclude_pattern" class="source-pattern small"><span class="muted">排除规则</span><code>{{ source.exclude_pattern }}</code></p><p v-if="source.last_error || rowErrors[source.id]" class="notice error" role="alert">{{ rowErrors[source.id] || source.last_error }}<span v-if="source.last_error && source.last_refresh_at">上次成功的节点内容已保留。</span></p><p v-else-if="source.status === 'queued' || source.status === 'processing'" class="small muted source-progress"><span class="spinner"></span>{{ source.status === 'queued' ? '等待后台任务处理，结果将自动更新…' : '正在解析并整理节点，结果将自动更新…' }}</p><p v-else-if="rowMessages[source.id]" class="small green-text">{{ rowMessages[source.id] }}</p></article></div>
    </div>
    <Modal v-if="importOpen" title="添加来源" :busy="importBusy" wide @close="importOpen = false"><div class="tab-bar" role="tablist" aria-label="导入方式"><button v-for="kind in (['url', 'file', 'links'] as const)" :key="kind" :class="{ active: importKind === kind }" role="tab" :aria-selected="importKind === kind" :disabled="importBusy" @click="importKind = kind">{{ kindLabels[kind] }}</button></div><form @submit.prevent="startImport"><fieldset :disabled="importBusy"><div class="form-grid"><label>来源名称 <span class="muted small">可选</span><input v-model="importName" placeholder="留空使用地址或文件名称" maxlength="200" /></label><label>名称排除正则 <span class="muted small">可选</span><input v-model="importPattern" placeholder="例如：剩余|流量|到期" /><span class="field-hint">与全局规则共同生效，匹配的节点不会进入有效列表。</span></label></div><label v-if="importKind === 'url'">订阅地址 <span class="muted small">每行一个</span><textarea v-model="urlText" rows="7" placeholder="https://example.com/subscription&#10;https://example.org/subscription" required spellcheck="false"></textarea><span class="field-hint">每个地址独立创建来源，个别失败不影响其他来源。</span></label><div v-else-if="importKind === 'file'"><label class="file-drop"><span class="empty-icon">↑</span><strong>选择本地文件</strong><span class="small muted">支持一次选择多个文件，读取文本后导入</span><input type="file" multiple @change="selectFiles" /></label><ul class="file-list"><li v-for="(file, index) in files" :key="index"><span>{{ file.name }} <small class="muted">{{ (file.size / 1024).toFixed(1) }} KB</small></span><button type="button" class="text-button danger" :aria-label="`移除 ${file.name}`" @click="files.splice(index, 1)">移除</button></li></ul></div><label v-else>节点分享链接<textarea v-model="linksText" rows="7" placeholder="粘贴 ss://、vmess://、vless://、trojan:// 等节点链接，每行一个" required spellcheck="false"></textarea><span class="field-hint">本次粘贴内容作为一个来源保存。</span></label></fieldset><div v-if="results.length" class="notice" role="status">本次任务：{{ finished }} 个就绪，{{ awaiting }} 个处理中，{{ failed }} 个失败。详细结果保留在来源列表上方。</div><p v-if="importError" class="notice error" role="alert">{{ importError }}</p><div class="dialog-actions"><button type="button" :disabled="importBusy" @click="importOpen = false">{{ results.length ? '返回来源列表' : '取消' }}</button><button class="primary" type="submit" :disabled="importBusy || !canImport"><span v-if="importBusy" class="spinner"></span>{{ importBusy ? '正在提交…' : '开始导入' }}</button></div></form></Modal>
    <Modal v-if="editing" title="编辑来源" :busy="editBusy" @close="editing = null"><form @submit.prevent="saveEdit"><fieldset :disabled="editBusy"><label>来源名称<input v-model="editForm.name" required maxlength="200" /></label><label>名称排除正则<input v-model="editForm.exclude_pattern" placeholder="留空则仅使用全局排除规则" /><span class="field-hint">修改规则后自动按已保存内容重新处理。</span></label><label v-if="editing.kind === 'url'">替换订阅地址 <span class="small muted">可选</span><input v-model="editForm.url" type="url" autocomplete="off" placeholder="留空保留原订阅地址" /><span class="field-hint">为保护订阅信息，已保存的地址不会回显。</span></label><label class="checkbox-label"><input v-model="editForm.enabled" type="checkbox" />启用此来源</label></fieldset><p v-if="editError" class="notice error" role="alert">{{ editError }}</p><div class="dialog-actions"><button type="button" :disabled="editBusy" @click="editing = null">取消</button><button type="submit" class="primary" :disabled="editBusy || !editForm.name.trim()">{{ editBusy ? '正在保存…' : '保存修改' }}</button></div></form></Modal>
    <Modal v-if="deleting" title="删除来源" :busy="deleteBusy" @close="deleting = null"><p>确定删除「{{ deleting.name }}」吗？此操作无法撤销。</p><p class="muted">仅由此来源提供的节点将移除，其他来源仍引用的节点会保留。</p><p v-if="deleteError" class="notice error" role="alert">{{ deleteError }}</p><div class="dialog-actions"><button :disabled="deleteBusy" @click="deleting = null">取消</button><button class="danger-solid" :disabled="deleteBusy" @click="removeSource">{{ deleteBusy ? '正在删除…' : '确认删除' }}</button></div></Modal>
  </section>
</template>
