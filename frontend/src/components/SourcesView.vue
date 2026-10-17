<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi, type NewSource, type Source } from '../api/system'
import { currentUser } from '../api/accounts'
import { useAccountScope } from '../community'
import SourceLevelSelect from './SourceLevelSelect.vue'
import SourceSubscribers from './SourceSubscribers.vue'
import SourceLinkDialog from './SourceLinkDialog.vue'
import UsageHint from './UsageHint.vue'
import '../community.css'
import Modal from './Modal.vue'
import FileDropZone from './FileDropZone.vue'
import AppIcon from './AppIcon.vue'
import TitleChip from './TitleChip.vue'
import ModeGuide from './ModeGuide.vue'
import PageNumbers from './PageNumbers.vue'
import UserAvatar from './UserAvatar.vue'
import FeedbackBadge from './FeedbackBadge.vue'
import FeedbackPanel from './FeedbackPanel.vue'
import TakedownDialog from './TakedownDialog.vue'
import { isStaff } from '../api/moderation'
import ProviderTrend from './ProviderTrend.vue'
import { navigate } from '../router'
import { route, routeParams, setRouteQuery } from '../router'
const props = defineProps<{ items: Source[]; loading: boolean; error: string }>()
const emit = defineEmits<{ reload: []; updated: [source: Source]; removed: [id: number] }>()
const api = useApi()
const scope = useAccountScope()
const subscriberSourceID = ref<number | null>(null)
const linkSourceID = ref<number | null>(null)
const subscriberSource = computed(() => props.items.find(source => source.id === subscriberSourceID.value))
const linkSource = computed(() => props.items.find(source => source.id === linkSourceID.value))
function canManage(source: Source) { return currentUser.value?.role === 'admin' || currentUser.value?.id === source.owner_id }
function linkUpdated(source: Source) { emit('updated', source); emit('reload') }
const search = ref(routeParams('sources').get('q') ?? '')
function viewSource(id: number) { navigate('nodes', new URLSearchParams({ source_id: String(id) })) }
watch(route, value => { if (value.section === 'sources') search.value = routeParams('sources').get('q') ?? '' }, { flush: 'sync' })
watch(search, value => {
  if (route.value.section !== 'sources') return
  const params = new URLSearchParams()
  if (value) params.set('q', value)
  setRouteQuery('sources', params, true)
})
const visibleSources = computed(() => props.items.filter(source => source.name.toLowerCase().includes(search.value.toLowerCase())))
const sourcePageSize = 12
const sourcePage = ref(1)
const sourcePages = computed(() => Math.max(1, Math.ceil(visibleSources.value.length / sourcePageSize)))
const pagedSources = computed(() => visibleSources.value.slice((Math.min(sourcePage.value, sourcePages.value) - 1) * sourcePageSize, Math.min(sourcePage.value, sourcePages.value) * sourcePageSize))
watch(search, () => { sourcePage.value = 1 })
function seatPercent(source: Source) { return source.max_users ? Math.min(100, Math.round((source.occupied_users ?? source.active_users ?? 0) / source.max_users * 100)) : 0 }
const packageSourceID = ref<number | null>(null)
const feedbackSourceID = ref<number | null>(null)
const moderateTarget = ref<Source | null>(null)
function moderated() { moderateTarget.value = null; emit('reload') }
const packageSource = computed(() => props.items.find(source => source.id === packageSourceID.value))
const feedbackSource = computed(() => props.items.find(source => source.id === feedbackSourceID.value))
// 本地文件与节点链接来源没有上游，直接替换保存的内容
const updating = ref<Source | null>(null)
const updateText = ref('')
const updateFile = ref<File | null>(null)
const updateBusy = ref(false)
const updateError = ref('')
function openUpdate(source: Source) { updating.value = source; updateText.value = ''; updateFile.value = null; updateError.value = '' }
async function submitUpdate() {
  const source = updating.value
  if (!source || updateBusy.value) return
  const ticket = scope.ticket()
  updateBusy.value = true; updateError.value = ''
  try {
    const content = source.kind === 'file' ? await updateFile.value?.text() ?? '' : updateText.value
    if (!content.trim()) throw new Error(source.kind === 'file' ? '请选择包含节点的文件。' : '请粘贴节点链接。')
    const updated = await api<Source>(`/sources/${source.id}`, json('PATCH', { content }))
    if (!scope.current(ticket)) return
    emit('updated', updated); emit('reload'); rowMessages[source.id] = '节点内容已更新，正在重新整理。'; updating.value = null
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) updateError.value = errorText(cause) }
  finally { updateBusy.value = false }
}
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
const importMaxUsers = ref(0)
const importMinLevel = ref(0)
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
const editForm = reactive({ name: '', exclude_pattern: '', url: '', enabled: true, max_users: 0, min_level: 0 })
const editBusy = ref(false)
const editError = ref('')
const deleting = ref<Source | null>(null)
const deleteBusy = ref(false)
const deleteError = ref('')
let active = true
onBeforeUnmount(() => { active = false })
watch(() => route.value.section, section => {
  if (section !== 'sources') { subscriberSourceID.value = null; linkSourceID.value = null; editing.value = null; deleting.value = null; importOpen.value = false; updating.value = null; packageSourceID.value = null; feedbackSourceID.value = null }
})
watch(() => props.items, items => {
  for (const result of results.value) {
    const source = items.find(item => item.id === result.id)
    if (source) { result.status = source.status; result.error = source.last_error || ''; result.name = source.name }
  }
}, { deep: true })
function addFiles(list: File[]) { files.value.push(...list) }
function validUrl(value: string) {
  try { return ['http:', 'https:'].includes(new URL(value).protocol) } catch { return false }
}
async function startImport() {
  if (!canImport.value || importBusy.value) return
  const ticket = scope.ticket()
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
    while (active && scope.current(ticket) && cursor < rows.length) {
      const index = cursor++
      const result = results.value[index]
      const value = rows[index]
      if (!result || value === undefined) continue
      try {
        const payload: NewSource = { name: result.name, kind, exclude_pattern: pattern, max_users: importMaxUsers.value, min_level: importMinLevel.value }
        if (kind === 'url') {
          if (!validUrl(value)) throw new Error(`第 ${index + 1} 行不是有效的 HTTP / HTTPS 订阅地址。`)
          payload.url = value
          if (!name) { result.name = `订阅 · ${new URL(value).hostname}`; payload.name = result.name }
        } else {
          const file = selectedFiles[index]
          payload.content = kind === 'file' && file ? await file.text() : pasted
          if (!payload.content.trim()) throw new Error('内容为空，请选择包含节点的文件或粘贴节点链接。')
        }
        if (!scope.current(ticket)) return
        const source = await api<Source>('/sources', json('POST', payload))
        if (!scope.current(ticket)) return
        result.id = source.id; result.name = source.name; result.status = source.status; result.error = source.last_error || ''
        emit('reload')
      } catch (error) {
        if (!scope.current(ticket) || isCancelled(error)) return
        result.status = 'error'; result.error = errorText(error)
      }
    }
  }
  try {
    await Promise.all(Array.from({ length: Math.min(3, rows.length) }, () => worker()))
    if (active && scope.current(ticket)) { emit('reload'); urlText.value = ''; linksText.value = ''; files.value = [] }
  } finally { importBusy.value = false }
}
function openEdit(source: Source) {
  editing.value = source
  Object.assign(editForm, { name: source.name, exclude_pattern: source.exclude_pattern, url: '', enabled: source.enabled, max_users: source.max_users ?? 0, min_level: source.min_level ?? 0 })
  editError.value = ''
}
async function saveEdit() {
  const source = editing.value
  if (!source || editBusy.value || !editForm.name.trim()) return
  const ticket = scope.ticket()
  editError.value = ''
  if (source.kind === 'url' && editForm.url.trim() && !validUrl(editForm.url.trim())) { editError.value = '请输入有效的 HTTP / HTTPS 订阅地址，留空则保留原地址。'; return }
  editBusy.value = true
  try {
    const updated = await api<Source>(`/sources/${source.id}`, json('PATCH', { name: editForm.name.trim(), enabled: editForm.enabled, exclude_pattern: editForm.exclude_pattern, max_users: editForm.max_users, min_level: editForm.min_level, ...(source.kind === 'url' && editForm.url.trim() ? { url: editForm.url.trim() } : {}) }))
    if (!scope.current(ticket)) return
    emit('updated', updated); emit('reload'); editing.value = null
  } catch (error) { if (scope.current(ticket) && !isCancelled(error)) editError.value = errorText(error) }
  finally { editBusy.value = false }
}
async function toggle(source: Source) {
  if (busyIds.has(source.id)) return
  const ticket = scope.ticket()
  busyIds.add(source.id); rowErrors[source.id] = ''; rowMessages[source.id] = ''
  try {
    const updated = await api<Source>(`/sources/${source.id}`, json('PATCH', { enabled: !source.enabled }))
    if (!scope.current(ticket)) return
    emit('updated', updated); emit('reload')
  } catch (error) { if (scope.current(ticket) && !isCancelled(error)) rowErrors[source.id] = errorText(error) }
  finally { busyIds.delete(source.id) }
}
async function refresh(source: Source) {
  if (busyIds.has(source.id)) return
  const ticket = scope.ticket()
  busyIds.add(source.id); rowErrors[source.id] = ''; rowMessages[source.id] = ''
  try {
    await api(`/sources/${source.id}/refresh`, json('POST'))
    if (!scope.current(ticket)) return
    emit('updated', { ...source, status: 'queued', last_error: '' })
    rowMessages[source.id] = '已加入刷新队列。'
    emit('reload')
  } catch (error) { if (scope.current(ticket) && !isCancelled(error)) rowErrors[source.id] = errorText(error) }
  finally { busyIds.delete(source.id) }
}
function confirmDelete(source: Source) { deleting.value = source; deleteError.value = '' }
async function removeSource() {
  const source = deleting.value
  if (!source || deleteBusy.value) return
  const ticket = scope.ticket()
  deleteBusy.value = true
  try {
    await api(`/sources/${source.id}`, json('DELETE'))
    if (!scope.current(ticket)) return
    emit('removed', source.id); emit('reload'); deleting.value = null
  } catch (error) { if (scope.current(ticket) && !isCancelled(error)) deleteError.value = errorText(error) }
  finally { deleteBusy.value = false }
}
const sourceGuide = [
  { icon: 'users', tone: 'tone-blue', title: '你提供，大家按席位使用', text: '添加的订阅会整理进节点池。其他用户把你来源的节点加入组合订阅时占用一个席位；你可以设置席位上限和最低等级，并在「订阅用户」里查看谁在使用。' },
  { icon: 'refresh', tone: 'tone-amber', title: '节点随来源变化', text: '上游刷新、停用或删除来源后，相关节点会同步从所有人的订阅中更新或移除。平台不保证节点长期可用，也不替任何人保存节点。' },
  { icon: 'shield', tone: 'tone-green', title: '原始订阅不会外发', text: '订阅地址、原始内容和节点配置只保存在服务端，启用数据密钥后加密存储；节点地址只有你和管理员可见，频繁更换节点会被风控限制。但节点连接信息仍会出现在使用者的客户端里，请只共享你愿意分享的订阅。' },
  { icon: 'clock', tone: 'tone-violet', title: '席位会自动释放', text: '使用者租期到期且未续期时，席位自动释放，历史记录不占名额。停用来源会让使用者暂时拿不到这些节点。' },
]
</script>

<template>
  <section>
    <div class="page-heading"><div><p class="eyebrow">SHARED SOURCES</p><h1>共享来源</h1><p class="muted">把你的订阅共享给大家，统一整理节点并保留每个节点的来处。</p></div><button class="primary" :disabled="importBusy" @click="importOpen = true"><AppIcon name="plus" />添加来源</button></div>
    <ModeGuide storage-key="nodepool-guide-sources" title="共享是怎么运作的" summary="你提供订阅，平台整理成节点池，其他用户按席位使用。共享前请先了解这几点。" :items="sourceGuide" />
    <div class="source-summary"><div><strong>{{ totalsKnown ? items.length : '—' }}</strong><span>全部来源</span></div><div><strong>{{ totalsKnown ? enabledCount : '—' }}</strong><span>已启用</span></div><div><strong>{{ totalsKnown ? pendingCount : '—' }}</strong><span>等待 / 处理中</span></div><div><strong :class="{ 'danger-text': failedCount > 0 }">{{ totalsKnown ? failedCount : '—' }}</strong><span>处理失败</span></div></div>
    <ProviderTrend v-if="items.some(source => source.owner_id === currentUser?.id)" :paused="route.section !== 'sources'" />
    <div v-if="results.length" class="panel import-results" aria-live="polite"><div class="panel-heading"><div><h2>本次导入进度</h2><p class="small muted">{{ finished }} 个已就绪 · {{ awaiting }} 个处理中 · <span :class="{ 'danger-text': failed > 0 }">{{ failed }} 个失败</span></p></div><button @click="resultsExpanded = !resultsExpanded">{{ resultsExpanded ? '收起' : '展开' }}</button></div><p v-if="failed" class="notice warning">{{ finished || awaiting ? '部分来源导入失败，其余来源继续处理。' : '本次来源未能导入。' }}请查看下方原因，可修正后重新添加或逐个重试。</p><ul v-if="resultsExpanded" class="result-list"><li v-for="result in results" :key="result.key"><span><strong>{{ result.name }}</strong><small v-if="result.error" class="danger-text">{{ result.error }}</small></span><span :class="['badge', result.status]">{{ result.status === 'submitting' ? '正在提交' : statusLabels[result.status] }}</span></li></ul></div>
    <div class="panel"><div class="panel-heading"><div><h2>来源列表 <span class="count">{{ items.length }}</span></h2><p class="small muted">刷新失败会保留上次成功内容，重复节点自动合并。</p></div><label class="search-field"><span class="sr-only">搜索来源名称</span><AppIcon name="search" /><input v-model="search" type="search" placeholder="搜索来源名称" /></label></div>
      <div v-if="error" class="notice error" role="alert">{{ error }}<span v-if="items.length">当前保留上次读取结果。</span><button :disabled="loading" @click="emit('reload')">重试</button></div>
      <div v-if="loading && !items.length" class="empty-state" role="status"><span class="spinner"></span><h3>正在读取来源</h3></div>
      <div v-else-if="!visibleSources.length && !error" class="empty-state"><span class="empty-icon"><AppIcon name="sources" /></span><h3>{{ search ? '没有匹配的来源' : '从第一个来源开始' }}</h3><p>{{ search ? '尝试其他名称。' : '支持批量订阅地址、多个本地文件和节点分享链接。' }}</p><button v-if="!search" class="primary" @click="importOpen = true"><AppIcon name="plus" />添加来源</button></div>
      <div v-else class="source-list" :aria-busy="loading"><article v-for="source in pagedSources" :key="source.id" :class="['source-card', { disabled: !source.enabled }]" :data-status="source.status" :data-kind="source.kind">
        <div class="source-card-head"><span class="source-icon" aria-hidden="true"><AppIcon :name="source.kind === 'url' ? 'external' : source.kind === 'file' ? 'upload' : 'link'" /></span><div class="source-title"><h3 :title="source.name">{{ source.name }}</h3><span class="small muted">{{ kindLabels[source.kind] }} · 加入于 {{ formatDate(source.created_at).split(' ')[0] }}</span></div><span :class="['badge', source.takedown ? 'error' : source.status]">{{ source.takedown ? '已下架' : source.enabled ? statusLabels[source.status] : '已停用' }}</span><button :class="['switch', { on: source.enabled }]" role="switch" :aria-checked="source.enabled" :aria-label="`${source.enabled ? '停用' : '启用'} ${source.name}`" :disabled="busyIds.has(source.id) || editBusy || deleteBusy || !!source.takedown" :title="source.takedown ? '已被管理员下架，需管理员恢复' : undefined" @click="toggle(source)"><span></span></button></div>
        <p v-if="source.takedown" class="source-takedown" role="status"><AppIcon name="alert" /><span><strong>已被管理员下架：{{ source.takedown.reason_label }}</strong><template v-if="source.takedown.detail"><br />{{ source.takedown.detail }}</template><small>{{ source.takedown.by || '管理员' }} · {{ formatDate(source.takedown.at) }}。如已处理，可通过“用户反馈”联系管理员恢复。</small></span></p>
        <div class="source-stats"><div><strong>{{ source.node_count }}</strong><span>有效节点</span></div><div><strong>{{ source.excluded_count }}</strong><span>已排除</span></div><div><strong>{{ source.occupied_users ?? source.active_users ?? 0 }}<small> / {{ source.max_users || '∞' }}</small></strong><span>有效席位</span><i v-if="source.max_users" class="seat-bar" role="meter" :aria-label="`席位占用 ${seatPercent(source)}%`" :aria-valuenow="seatPercent(source)" aria-valuemin="0" aria-valuemax="100"><span :style="{ width: `${seatPercent(source)}%` }" :data-full="seatPercent(source) >= 100"></span></i></div><div><strong>{{ source.min_level ? `Lv${source.min_level}` : '不限' }}</strong><span>等级门槛</span></div></div>
        <button v-if="source.subscription_usage" type="button" class="usage-button" :aria-label="`查看 ${source.name} 的套餐详情`" @click="packageSourceID = source.id"><UsageHint :usage="source.subscription_usage" bar /></button>
        <div class="source-schedule"><span>{{ source.kind === 'url' ? '最近刷新' : '最近解析' }} <b>{{ formatDate(source.last_refresh_at) }}</b></span><span v-if="source.kind === 'url'">下次刷新 <b>{{ source.enabled ? formatDate(source.next_refresh_at) : '已暂停' }}</b></span><span v-else>内容由你手动更新</span></div>
        <p v-if="source.exclude_pattern" class="source-pattern small"><span class="muted">排除规则</span><code>{{ source.exclude_pattern }}</code></p><p v-if="source.last_error || rowErrors[source.id]" class="notice error" role="alert">{{ rowErrors[source.id] || source.last_error }}<span v-if="source.last_error && source.last_refresh_at">上次成功的节点内容已保留。</span></p><p v-else-if="source.status === 'queued' || source.status === 'processing'" class="small muted source-progress"><span class="spinner"></span>{{ source.status === 'queued' ? '等待后台任务处理，结果将自动更新…' : '正在解析并整理节点，结果将自动更新…' }}</p><p v-else-if="rowMessages[source.id]" class="small green-text">{{ rowMessages[source.id] }}</p>
        <div class="source-card-foot">
          <span class="source-provider"><UserAvatar :id="source.owner_id" :name="source.provider_name || source.provider_username || '管理员'" :version="source.provider_avatar" :size="26" /><span>{{ source.provider_name || source.provider_username || '管理员' }}</span><TitleChip :name="source.provider_title" /></span>
          <button type="button" class="feedback-link" :aria-label="`查看 ${source.name} 的用户反馈`" @click="feedbackSourceID = source.id"><FeedbackBadge v-if="source.feedback?.total" :summary="source.feedback" /><span v-else>暂无反馈</span><AppIcon name="right" /></button>
        </div>
        <div class="source-actions">
          <button v-if="source.kind === 'url'" class="compact" :disabled="busyIds.has(source.id) || !source.enabled || source.status === 'queued' || source.status === 'processing'" @click="refresh(source)"><AppIcon name="refresh" />刷新</button>
          <button v-else class="compact" :disabled="busyIds.has(source.id) || source.status === 'queued' || source.status === 'processing'" @click="openUpdate(source)"><AppIcon name="upload" />更新节点</button>
          <button class="compact" @click="viewSource(source.id)"><AppIcon name="nodes" />节点</button>
          <template v-if="canManage(source)"><button class="compact" @click="subscriberSourceID = source.id"><AppIcon name="users" />订阅用户</button><button v-if="source.share_url" class="compact" @click="linkSourceID = source.id"><AppIcon name="link" />直链</button></template>
          <button v-if="isStaff" class="compact" :class="{ danger: !source.takedown }" @click="moderateTarget = source"><AppIcon :name="source.takedown ? 'unlock' : 'lock'" />{{ source.takedown ? '恢复' : '下架' }}</button>
          <span class="source-actions-spacer"></span>
          <button class="icon-button" aria-label="编辑" :title="`编辑 ${source.name}`" :disabled="busyIds.has(source.id)" @click="openEdit(source)"><AppIcon name="edit" /></button>
          <button class="icon-button danger" aria-label="删除" :title="`删除 ${source.name}`" :disabled="busyIds.has(source.id)" @click="confirmDelete(source)"><AppIcon name="trash" /></button>
        </div>
      </article></div>
      <div v-if="visibleSources.length > sourcePageSize" class="pagination"><span class="small muted">共 {{ visibleSources.length }} 个来源</span><PageNumbers v-model="sourcePage" :pages="sourcePages" previous-label="上一页来源" next-label="下一页来源" /></div>
    </div>
    <Modal v-if="importOpen" title="添加来源" :busy="importBusy" wide @close="importOpen = false">
      <div class="tab-bar" role="tablist" aria-label="导入方式"><button v-for="kind in (['url', 'file', 'links'] as const)" :key="kind" :class="{ active: importKind === kind }" role="tab" :aria-selected="importKind === kind" :disabled="importBusy" @click="importKind = kind">{{ kindLabels[kind] }}</button></div>
      <form @submit.prevent="startImport"><fieldset :disabled="importBusy">
        <div class="form-grid"><label class="form-field"><span class="field-label">来源名称 <small class="muted">可选</small></span><input v-model="importName" placeholder="留空使用地址或文件名称" maxlength="200" /></label><label class="form-field"><span class="field-label">名称排除正则 <small class="muted">可选</small></span><input v-model="importPattern" placeholder="例如：剩余|流量|到期" /><span class="field-hint">与全局规则共同生效，匹配的节点不会进入有效列表。</span></label></div>
        <label class="form-field"><span class="field-label">最大有效订阅人数</span><input v-model.number="importMaxUsers" type="number" min="0" max="100000000" step="1" required /><span class="field-hint">0 表示不限，不限人数的来源也不占用户的来源额度。同一账号引用多个节点只占一席，租期到期后自动释放。</span></label>
        <SourceLevelSelect v-model="importMinLevel" :disabled="importBusy" />
        <label v-if="importKind === 'url'" class="form-field"><span class="field-label">订阅地址 <small class="muted">每行一个</small></span><textarea v-model="urlText" rows="7" placeholder="https://example.com/subscription&#10;https://example.org/subscription" required spellcheck="false"></textarea><span class="field-hint">每个地址独立创建来源，个别失败不影响其他来源。</span></label>
        <div v-else-if="importKind === 'file'"><FileDropZone title="添加本地节点文件" hint="把 YAML、TXT 等订阅文件拖到这里，支持多个文件" button="选择文件" multiple @files="addFiles" /><ul class="file-list"><li v-for="(file, index) in files" :key="index"><span>{{ file.name }} <small class="muted">{{ (file.size / 1024).toFixed(1) }} KB</small></span><button type="button" class="text-button danger" :aria-label="`移除 ${file.name}`" @click="files.splice(index, 1)">移除</button></li></ul></div>
        <label v-else class="form-field"><span class="field-label">节点分享链接</span><textarea v-model="linksText" rows="7" placeholder="粘贴 ss://、vmess://、vless://、trojan:// 等节点链接，每行一个" required spellcheck="false"></textarea><span class="field-hint">本次粘贴内容作为一个来源保存。</span></label>
      </fieldset><div v-if="results.length" class="notice" role="status">本次任务：{{ finished }} 个就绪，{{ awaiting }} 个处理中，{{ failed }} 个失败。详细结果保留在来源列表上方。</div><p v-if="importError" class="notice error" role="alert">{{ importError }}</p><div class="dialog-actions"><button type="button" :disabled="importBusy" @click="importOpen = false">{{ results.length ? '返回来源列表' : '取消' }}</button><button class="primary" type="submit" :disabled="importBusy || !canImport"><span v-if="importBusy" class="spinner"></span>{{ importBusy ? '正在提交…' : '开始导入' }}</button></div></form>
    </Modal>
    <Modal v-if="editing" title="编辑来源" :busy="editBusy" @close="editing = null"><form @submit.prevent="saveEdit"><fieldset :disabled="editBusy">
      <label class="form-field"><span class="field-label">来源名称</span><input v-model="editForm.name" required maxlength="200" /></label>
      <label class="form-field"><span class="field-label">名称排除正则</span><input v-model="editForm.exclude_pattern" placeholder="留空则仅使用全局排除规则" /><span class="field-hint">修改规则后自动按已保存内容重新处理。</span></label>
      <label class="form-field"><span class="field-label">最大有效订阅人数</span><input v-model.number="editForm.max_users" type="number" min="0" max="100000000" step="1" required /><span class="field-hint">有效席位 {{ editing.occupied_users ?? editing.active_users ?? 0 }} 人；0 表示不限。历史记录不占名额。</span></label>
      <SourceLevelSelect v-model="editForm.min_level" :disabled="editBusy" />
      <label v-if="editing.kind === 'url'" class="form-field"><span class="field-label">替换订阅地址 <small class="muted">可选</small></span><input v-model="editForm.url" type="url" autocomplete="off" placeholder="留空保留原订阅地址" /><span class="field-hint">为保护订阅信息，已保存的地址不会回显。</span></label>
      <label class="checkbox-label"><input v-model="editForm.enabled" type="checkbox" />启用此来源</label>
    </fieldset><p v-if="editError" class="notice error" role="alert">{{ editError }}</p><div class="dialog-actions"><button type="button" :disabled="editBusy" @click="editing = null">取消</button><button type="submit" class="primary" :disabled="editBusy || !editForm.name.trim()">{{ editBusy ? '正在保存…' : '保存修改' }}</button></div></form></Modal>
    <Modal v-if="deleting" title="删除来源" :busy="deleteBusy" @close="deleting = null"><p>确定删除「{{ deleting.name }}」吗？此操作无法撤销。</p><p class="muted">仅由此来源提供的节点将移除，其他来源仍引用的节点会保留。</p><p v-if="deleteError" class="notice error" role="alert">{{ deleteError }}</p><div class="dialog-actions"><button :disabled="deleteBusy" @click="deleting = null">取消</button><button class="danger-solid" :disabled="deleteBusy" @click="removeSource">{{ deleteBusy ? '正在删除…' : '确认删除' }}</button></div></Modal>
    <Modal v-if="updating" title="更新节点" :busy="updateBusy" @close="updating = null"><form @submit.prevent="submitUpdate">
      <p class="muted">用新内容替换「{{ updating.name }}」保存的全部节点。仍存在的节点保持原有订阅关系，新内容中没有的节点会从所有订阅里移除。</p>
      <FileDropZone v-if="updating.kind === 'file'" :title="updateFile ? updateFile.name : '选择新的节点文件'" :hint="updateFile ? `${(updateFile.size / 1024).toFixed(1)} KB · 可拖入其他文件替换` : '拖入文件或点击选择，读取文本后整体替换'" :button="updateFile ? '重新选择' : '选择文件'" :disabled="updateBusy" @files="updateFile = $event[0] ?? null" />
      <label v-else class="form-field"><span class="field-label">节点分享链接</span><textarea v-model="updateText" rows="8" spellcheck="false" placeholder="粘贴 ss://、vmess://、vless://、trojan:// 等节点链接，每行一个" :disabled="updateBusy"></textarea></label>
      <p v-if="updateError" class="notice error" role="alert">{{ updateError }}</p>
      <div class="dialog-actions"><button type="button" :disabled="updateBusy" @click="updating = null">取消</button><button class="primary" type="submit" :disabled="updateBusy || (updating.kind === 'file' ? !updateFile : !updateText.trim())">{{ updateBusy ? '正在更新…' : '替换并更新' }}</button></div>
    </form></Modal>
    <Modal v-if="packageSource" title="套餐详情" @close="packageSourceID = null"><h3 class="ellipsis" :title="packageSource.name">{{ packageSource.name }}</h3><UsageHint :usage="packageSource.subscription_usage" detail /></Modal>
    <Modal v-if="feedbackSource" title="来源反馈" wide @close="feedbackSourceID = null"><h3 class="ellipsis" :title="feedbackSource.name">{{ feedbackSource.name }}</h3><FeedbackPanel :target="canManage(feedbackSource) ? 'all' : 'source'" :source-id="feedbackSource.id" :can-submit="!canManage(feedbackSource)" @changed="emit('reload')" /></Modal>
    <SourceSubscribers v-if="subscriberSource && route.section === 'sources'" :key="subscriberSource.id" :source="subscriberSource" @close="subscriberSourceID = null" @changed="emit('reload')" />
    <SourceLinkDialog v-if="linkSource && canManage(linkSource) && route.section === 'sources'" :key="linkSource.id" :source="linkSource" @close="linkSourceID = null" @updated="linkUpdated" />
    <TakedownDialog v-if="moderateTarget" :source="moderateTarget" @close="moderateTarget = null" @done="moderated" />
  </section>
</template>
