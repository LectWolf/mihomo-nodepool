<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi, type NodePage, type Options, type PoolNode, type Source } from '../api/system'
import { currentUser, isAdmin, profile, draftNodes, draftSources, draftName, draftEstimate, selectedSources, editingSubscription, selectionError, toggleNode, toggleSource, resetDraft, saveDraft, loadProfile } from '../api/accounts'
import { usePolling } from '../polling'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import CustomSelect from './CustomSelect.vue'
import NodeLocation from './NodeLocation.vue'
import CountryFilter from './CountryFilter.vue'
import FilterPopover from './FilterPopover.vue'
import SourceBrowser from './SourceBrowser.vue'
import { navigate, route, routeParams, setRouteQuery } from '../router'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const data = ref<NodePage | null>(null)
const loading = ref(false)
const error = ref('')
const options = ref<Options>({ types: [], countries: [] })
const optionsError = ref('')
const filters = reactive({ q: '', type: '', countries: '', source_id: '', status: '', provider_id: '', following: '', sort: 'latency' })
const countries = computed({ get: () => filters.countries ? filters.countries.split(',') : [], set: (value: string[]) => { filters.countries = value.join(',') } })
const sourceName = ref('')
const sourceText = computed(() => filters.source_id ? sourceName.value || `来源 #${filters.source_id}` : '全部来源')
const page = ref(1)
const pageSize = ref(50)
const layout = ref<'list' | 'cards'>('cards')
const nodeTesting = reactive(new Set<number>())
const allTesting = ref(false)
const actionError = ref('')
const actionMessage = ref('')
const details = ref<PoolNode | null>(null)
const originNode = ref<PoolNode | null>(null)
const sourcesOpen = ref(false)
const reviewOpen = ref(false)
const saving = ref(false)
const saveError = ref('')
const selectedIDs = computed(() => new Set(draftNodes.value.map(n => n.node_id)))
const wholeSourceIDs = computed(() => draftSources.value.map(s => s.id))
const hasDraft = computed(() => draftNodes.value.length > 0 || draftSources.value.length > 0 || editingSubscription.value !== null)
const maxPages = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / pageSize.value)))
const statusLabels = { healthy: '健康', unhealthy: '不可用', unknown: '待检测' }
const protocolOptions = computed(() => [{ value: '', label: '全部协议' }, ...options.value.types.map(type => ({ value: type, label: type.toUpperCase() }))])
const healthOptions = [{ value: '', label: '全部状态' }, { value: 'healthy', label: '健康' }, { value: 'unhealthy', label: '不可用' }, { value: 'unknown', label: '待检测' }]
const sortOptions = [{ value: 'latency', label: '延迟优先' }, { value: 'name', label: '按名称排序' }, { value: 'created', label: '按加入时间' }]
const sizeOptions = [25, 50, 100].map(value => ({ value, label: String(value) }))
let debounce: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
let sequence = 0
let restoringRoute = false
let replaceNextRoute = false
const query = computed(() => {
  const params = new URLSearchParams({ page: String(page.value), page_size: String(pageSize.value), sort: filters.sort })
  for (const [key, value] of Object.entries(filters)) if (key !== 'sort' && value.trim()) params.set(key, value.trim())
  return params.toString()
})
function pageParams() {
  const params = new URLSearchParams()
  if (page.value !== 1) params.set('page', String(page.value))
  if (pageSize.value !== 50) params.set('page_size', String(pageSize.value))
  if (filters.sort !== 'latency') params.set('sort', filters.sort)
  if (layout.value !== 'cards') params.set('view', layout.value)
  for (const [key, value] of Object.entries(filters)) if (key !== 'sort' && value.trim()) params.set(key, value.trim())
  return params
}
function restoreRoute() {
  if (route.value.section !== 'nodes') return
  const params = routeParams('nodes'); restoringRoute = true
  const size = Number(params.get('page_size'))
  pageSize.value = [25, 50, 100].includes(size) ? size : 50
  const requestedPage = Number(params.get('page'))
  page.value = Number.isInteger(requestedPage) && requestedPage > 0 && requestedPage <= 1000000 ? requestedPage : 1
  const positiveID = (key: string) => { const n = Number(params.get(key)); return Number.isSafeInteger(n) && n > 0 ? String(n) : '' }
  Object.assign(filters, {
    q: params.get('q') ?? '', type: params.get('type') ?? '',
    countries: [...new Set((params.get('countries') || params.get('country') || '').toUpperCase().split(',').filter(v => /^[A-Z]{2}$/.test(v)))].join(','),
    source_id: positiveID('source_id'), provider_id: positiveID('provider_id'), following: params.get('following') === '1' ? '1' : '',
    status: ['healthy', 'unhealthy', 'unknown'].includes(params.get('status') ?? '') ? params.get('status') : '',
    sort: ['latency', 'name', 'created'].includes(params.get('sort') ?? '') ? params.get('sort') : 'latency',
  })
  layout.value = params.get('view') === 'list' ? 'list' : 'cards'
  restoringRoute = false; setRouteQuery('nodes', pageParams(), true)
}
async function load() {
  if (props.paused) return
  const currentSequence = ++sequence
  controller?.abort(); controller = new AbortController(); loading.value = true
  try {
    const result = await api<NodePage>(`/nodes?${query.value}`, { signal: controller.signal })
    if (currentSequence !== sequence) return
    const last = Math.max(1, Math.ceil(result.total / pageSize.value))
    if (page.value > last) { replaceNextRoute = true; page.value = last; return }
    data.value = result; error.value = ''
    if (details.value) details.value = result.items.find(item => item.id === details.value?.id) ?? details.value
  } catch (cause) { if (!isCancelled(cause) && currentSequence === sequence) error.value = errorText(cause) }
  finally { if (currentSequence === sequence) loading.value = false }
}
async function loadOptions() {
  try { options.value = await api<Options>('/options'); optionsError.value = '' }
  catch (cause) { if (!isCancelled(cause)) optionsError.value = errorText(cause) }
}
watch(filters, () => { if (!restoringRoute) page.value = 1 }, { flush: 'sync' })
watch(pageSize, () => { if (!restoringRoute) page.value = 1 }, { flush: 'sync' })
watch([query, layout], ([next], [previous]) => {
  if (restoringRoute || route.value.section !== 'nodes') return
  const typing = new URLSearchParams(previous).get('q') !== new URLSearchParams(next).get('q')
  setRouteQuery('nodes', pageParams(), typing || replaceNextRoute); replaceNextRoute = false
})
watch(route, restoreRoute, { flush: 'sync', immediate: true })
watch(query, () => {
  ++sequence; controller?.abort(); clearTimeout(debounce)
  if (!props.paused) { loading.value = true; debounce = setTimeout(() => void load(), 300) }
})
watch(() => props.paused, paused => {
  if (paused) { ++sequence; clearTimeout(debounce); controller?.abort(); loading.value = false; details.value = null; originNode.value = null; sourcesOpen.value = false; reviewOpen.value = false }
  else { void load(); void loadOptions() }
})
usePolling(async () => { if (!loading.value && !props.paused) await load() }, 5000)
usePolling(async () => { if (!props.paused) await loadOptions() }, 20000)
onBeforeUnmount(() => { ++sequence; clearTimeout(debounce); controller?.abort() })
function viewProvider(id: number) { navigate('nodes', new URLSearchParams({ provider_id: String(id) })); details.value = null }
function reset() { Object.assign(filters, { q: '', type: '', countries: '', source_id: '', status: '', provider_id: '', following: '', sort: 'latency' }); sourceName.value = ''; page.value = 1 }
function chooseFilter(source: Source, close: () => void) { sourceName.value = source.name; filters.source_id = String(source.id); close() }
function canTest(node: PoolNode) { return isAdmin.value || node.sources.some(s => s.owner_id === currentUser.value?.id) }
async function testNode(node: PoolNode) {
  if (nodeTesting.has(node.id) || allTesting.value) return
  nodeTesting.add(node.id); actionError.value = ''; actionMessage.value = ''
  try { await api(`/nodes/${node.id}/test`, json('POST')); actionMessage.value = `「${node.name}」已加入检测队列，结果将自动更新。` }
  catch (cause) { if (!isCancelled(cause)) actionError.value = errorText(cause) }
  finally { nodeTesting.delete(node.id) }
}
async function testAll() {
  if (allTesting.value || !data.value?.stats.total) return
  allTesting.value = true; actionError.value = ''; actionMessage.value = ''
  try { await api('/nodes/test', json('POST', {})); actionMessage.value = '全部有效节点已加入检测队列，结果将自动更新。' }
  catch (cause) { if (!isCancelled(cause)) actionError.value = errorText(cause) }
  finally { allTesting.value = false }
}
function origins(node: PoolNode) {
  const scoped = node.sources.filter(s => (!filters.source_id || s.id === Number(filters.source_id)) && (!filters.provider_id || s.owner_id === Number(filters.provider_id)))
  return scoped.length ? scoped : node.sources
}
function chooseNode(node: PoolNode, event: Event) {
  const checkbox = event.target as HTMLInputElement
  checkbox.checked = selectedIDs.value.has(node.id)
  const existing = draftNodes.value.find(n => n.node_id === node.id)
  if (existing) { toggleNode(existing); return }
  const sources = origins(node)
  if (sources.length > 1) { originNode.value = node; return }
  if (sources[0]) toggleNode({ node_id: node.id, source_id: sources[0].id, name: node.name })
}
function chooseOrigin(sourceID: number) {
  const node = originNode.value; if (!node) return
  toggleNode({ node_id: node.id, source_id: sourceID, name: node.name }); originNode.value = null
}
function selectSource(source: Source) { toggleSource(source) }
async function save() {
  if (saving.value || !draftName.value.trim()) return
  saving.value = true; saveError.value = ''
  try {
    const result = await saveDraft()
    actionMessage.value = result.truncated ? `组合订阅已保存，整来源按剩余额度截取，共 ${result.selected_count} 个节点。` : `组合订阅已保存，共 ${result.selected_count} 个节点。`
    resetDraft(); reviewOpen.value = false
    try { await loadProfile() } catch (cause) { if (!isCancelled(cause)) actionError.value = `订阅已保存，用量读取失败：${errorText(cause)}` }
  } catch (cause) { if (!isCancelled(cause)) saveError.value = errorText(cause) }
  finally { saving.value = false }
}
</script>

<template>
  <section>
    <div class="page-heading"><div><p class="eyebrow">NODE OVERVIEW</p><h1>节点总览</h1><p class="muted">汇集唯一节点，选择你需要的连接。</p></div><button v-if="isAdmin" class="primary" :disabled="!data?.stats.total || allTesting || nodeTesting.size > 0 || paused" @click="testAll"><span v-if="allTesting" class="spinner"></span><AppIcon name="probe" /><span>{{ allTesting ? '正在排队' : '检测全部节点' }}</span></button></div>
    <div class="stats-grid">
      <article class="stat-card"><span><AppIcon name="nodes" />有效节点</span><strong>{{ data?.stats.total?.toLocaleString() ?? '—' }}</strong></article>
      <article class="stat-card"><span><span class="dot"></span>健康节点</span><strong class="green-text">{{ data?.stats.healthy?.toLocaleString() ?? '—' }}</strong></article>
      <article class="stat-card"><span><span class="dot bad"></span>不可用</span><strong>{{ data?.stats.unhealthy?.toLocaleString() ?? '—' }}</strong></article>
      <article class="stat-card"><span><span class="dot pending"></span>待检测</span><strong>{{ data?.stats.pending?.toLocaleString() ?? '—' }}</strong></article>
    </div>
    <div class="directory-toolbar node-scope"><div class="scope-tabs" role="group" aria-label="节点范围"><button :aria-pressed="!filters.following && !filters.provider_id" @click="filters.following = ''; filters.provider_id = ''; filters.source_id = ''">全部节点</button><button :aria-pressed="!!filters.following" @click="filters.following = filters.following ? '' : '1'; filters.provider_id = ''; filters.source_id = ''"><AppIcon name="heart" />已关注</button><span v-if="filters.provider_id" class="provider-scope">提供方 #{{ filters.provider_id }}<button class="icon-button" aria-label="取消提供方筛选" @click="filters.provider_id = ''; filters.source_id = ''"><AppIcon name="close" /></button></span></div><button @click="sourcesOpen = true"><AppIcon name="plus" />选择整个来源</button></div>
    <div v-if="hasDraft" class="selection-bar"><div><strong>{{ editingSubscription ? '编辑组合' : '已选节点' }}</strong><span>{{ draftNodes.length }} 个单选 · {{ draftSources.length }} 个整来源</span><small class="muted">账号额度 {{ currentUser?.node_limit ?? 200 }} 节点 / {{ currentUser?.source_limit ?? 3 }} 来源</small></div><button class="text-button" @click="resetDraft">清空</button><button class="primary" @click="saveError = ''; reviewOpen = true">{{ editingSubscription ? '保存修改' : '保存组合' }}<AppIcon name="right" /></button></div>
    <p v-if="selectionError" class="notice warning" role="alert">{{ selectionError }}</p>
    <div v-if="actionError" class="notice error" role="alert">{{ actionError }}</div><div v-if="actionMessage" class="notice success" role="status">{{ actionMessage }}<button @click="navigate('subscriptions')">我的订阅</button></div>
    <div class="panel node-panel">
      <div class="panel-heading node-toolbar"><h2>所有节点 <span class="count">{{ data?.total ?? '—' }}</span></h2><div class="node-toolbar-actions"><div class="view-switch" role="group" aria-label="节点展示方式"><button type="button" :aria-pressed="layout === 'cards'" aria-label="卡片视图" title="卡片视图" @click="layout = 'cards'"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M2 2h4v4H2zm8 0h4v4h-4zM2 10h4v4H2zm8 0h4v4h-4z" /></svg></button><button type="button" :aria-pressed="layout === 'list'" aria-label="列表视图" title="列表视图" @click="layout = 'list'"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M2 3h2m2 0h8M2 8h2m2 0h8M2 13h2m2 0h8" /></svg></button></div><button type="button" class="icon-button" aria-label="刷新节点" title="刷新节点" :disabled="loading || paused" @click="load"><span v-if="loading" class="spinner"></span><AppIcon v-else name="refresh" /></button></div></div>
      <div v-if="!paused" class="filters">
        <label class="search-field"><span class="sr-only">搜索节点</span><AppIcon name="search" /><input v-model="filters.q" type="search" placeholder="搜索名称或地址" /></label>
        <CustomSelect v-model="filters.type" label="协议" :options="protocolOptions" />
        <FilterPopover label="来源" :text="sourceText" :active="!!filters.source_id"><template #default="{ close }"><SourceBrowser mode="filter" :provider-id="filters.provider_id" :following="!!filters.following" :selected="filters.source_id ? [Number(filters.source_id)] : []" @choose="chooseFilter($event, close)" @clear="filters.source_id = ''; sourceName = ''; close()" /></template></FilterPopover>
        <CountryFilter v-model="countries" :options="options.countries" />
        <CustomSelect v-model="filters.status" label="健康状态" :options="healthOptions" />
        <CustomSelect v-model="filters.sort" label="排序" :options="sortOptions" />
        <button type="button" class="icon-button" aria-label="重置筛选" title="重置筛选" @click="reset"><AppIcon name="reset" /></button>
      </div>
      <div v-if="optionsError" class="notice warning small">筛选选项读取失败：{{ optionsError }}<button @click="loadOptions">重试选项</button></div>
      <div v-if="error" class="notice error" role="alert">{{ error }}<span v-if="data">当前保留上次读取结果。</span><button :disabled="loading" @click="load">重试</button></div>
      <div :class="layout === 'list' ? 'table-wrap' : 'node-cards-wrap'" :aria-busy="loading">
        <table v-if="data?.items.length && layout === 'list'" class="node-table"><thead><tr><th><span class="sr-only">选择</span></th><th>节点名称 / 地址</th><th>协议</th><th>出口 IP / 地区</th><th>延迟 / 状态</th><th>关联来源</th><th>最近检测</th><th class="right">操作</th></tr></thead><tbody>
          <tr v-for="node in data.items" :key="node.id" :data-node-id="node.id">
            <td><input class="node-checkbox" type="checkbox" :checked="selectedIDs.has(node.id)" :aria-label="`选择 ${node.name}`" @change="chooseNode(node, $event)" /></td>
            <td><button class="node-name text-button" @click="details = node">{{ node.name }}</button><span class="cell-sub mono">{{ node.server }}:{{ node.port }}</span></td><td><span class="protocol-tag">{{ node.type.toUpperCase() }}</span></td><td><NodeLocation :node="node" /></td>
            <td><span :class="['latency', { 'green-text': node.status === 'healthy' }]">{{ node.latency_ms === null ? '—' : `${node.latency_ms} ms` }}</span><span :class="['cell-sub', 'status-text', node.status]"><span class="dot" :class="{ bad: node.status === 'unhealthy', pending: node.status === 'unknown' }"></span>{{ statusLabels[node.status] }}</span></td><td><button class="source-association" @click="details = node">{{ node.sources[0]?.name || '—' }}<span v-if="node.sources.length > 1" class="count">+{{ node.sources.length - 1 }}</span></button></td><td><span class="small">{{ formatDate(node.last_checked_at) }}</span></td>
            <td class="right"><button v-if="canTest(node)" class="compact" :disabled="nodeTesting.has(node.id) || allTesting || paused" @click="testNode(node)"><AppIcon name="probe" /><span>{{ nodeTesting.has(node.id) ? '排队中…' : '检测' }}</span></button><button v-else class="compact" @click="details = node">详情</button></td>
          </tr>
        </tbody></table>
        <div v-else-if="data?.items.length" class="node-cards"><article v-for="node in data.items" :key="node.id" class="node-card" :class="{ selected: selectedIDs.has(node.id) }" :data-node-id="node.id">
          <div class="node-card-heading"><input class="node-checkbox" type="checkbox" :checked="selectedIDs.has(node.id)" :aria-label="`选择 ${node.name}`" @change="chooseNode(node, $event)" /><button class="node-name text-button" @click="details = node">{{ node.name }}</button><span class="protocol-tag">{{ node.type.toUpperCase() }}</span></div><p class="node-card-address mono">{{ node.server }}:{{ node.port }}</p><NodeLocation :node="node" />
          <div class="node-card-health"><span :class="['latency', { 'green-text': node.status === 'healthy' }]">{{ node.latency_ms === null ? '—' : `${node.latency_ms} ms` }}</span><span class="status-text"><span class="dot" :class="{ bad: node.status === 'unhealthy', pending: node.status === 'unknown' }"></span>{{ statusLabels[node.status] }}</span></div><div class="node-card-source"><span class="small muted">关联来源</span><button class="source-association" @click="details = node">{{ node.sources[0]?.name || '—' }}<span v-if="node.sources.length > 1" class="count">+{{ node.sources.length - 1 }}</span></button></div>
          <div class="node-card-actions"><button class="compact" :aria-label="`查看 ${node.name} 的详情`" @click="details = node">详情</button><button v-if="canTest(node)" class="compact" :disabled="nodeTesting.has(node.id) || allTesting || paused" :aria-label="`检测 ${node.name}`" @click="testNode(node)"><AppIcon name="probe" /><span>{{ nodeTesting.has(node.id) ? '排队中…' : '检测' }}</span></button></div>
        </article></div>
        <div v-else-if="loading && !data" class="empty-state" role="status"><span class="spinner"></span><h3>正在读取节点</h3><p>等待服务返回最新列表…</p></div>
        <div v-else-if="!error" class="empty-state"><span class="empty-icon"><AppIcon name="nodes" /></span><h3>{{ data?.stats.total ? '没有符合条件的节点' : '还没有有效节点' }}</h3><p>{{ data?.stats.total ? '尝试调整搜索词或筛选条件。' : '前往来源管理，导入订阅、本地文件或节点链接。' }}</p><button v-if="data?.stats.total" @click="reset">清空筛选</button></div>
      </div>
      <div class="pagination"><span class="small muted">{{ loading ? '正在更新…' : `共 ${data?.total ?? 0} 个匹配节点` }}</span><div><div class="inline-label"><span>每页</span><CustomSelect v-model="pageSize" label="每页条数" :options="sizeOptions" /></div><button :disabled="page <= 1 || loading || !data?.total" aria-label="上一页" title="上一页" @click="page--"><AppIcon name="left" /></button><span class="small">{{ page }} / {{ maxPages }}</span><button :disabled="page >= maxPages || loading || !data?.total" aria-label="下一页" title="下一页" @click="page++"><AppIcon name="right" /></button></div></div>
    </div>
    <Modal v-if="details" title="节点详情" @close="details = null"><div class="detail-title"><h3>{{ details.name }}</h3><span class="protocol-tag">{{ details.type.toUpperCase() }}</span></div><dl class="detail-list"><div><dt>连接地址</dt><dd class="mono">{{ details.server }}:{{ details.port }}</dd></div><div><dt>状态</dt><dd>{{ statusLabels[details.status] }} · {{ details.latency_ms === null ? '暂无延迟' : `${details.latency_ms} ms` }}</dd></div><div><dt>出口 / 地区</dt><dd><NodeLocation :node="details" /></dd></div><div><dt>最近检测</dt><dd>{{ formatDate(details.last_checked_at) }}</dd></div></dl><p v-if="details.last_error" class="notice error">{{ details.last_error }}</p><h3>关联来源 <span class="count">{{ details.sources.length }}</span></h3><ul class="association-list"><li v-for="source in details.sources" :key="source.id"><strong>{{ source.name }}</strong><span class="small muted">原始名称：{{ source.node_name }}</span><button v-if="source.owner_id" class="text-button" @click="viewProvider(source.owner_id)">{{ source.provider_name || '查看提供方' }}<AppIcon name="right" /></button></li></ul></Modal>
    <Modal v-if="originNode" title="选择领取来源" @close="originNode = null"><p class="muted">「{{ originNode.name }}」由多个来源提供，请选择本次领取归属。</p><div class="origin-choices"><button v-for="source in origins(originNode)" :key="source.id" class="source-choice" @click="chooseOrigin(source.id)"><AppIcon name="sources" /><span><strong>{{ source.name }}</strong><small>{{ source.provider_name || '提供方' }}</small></span><AppIcon name="right" /></button></div><p class="small muted">同一账号从同一来源领取多个节点，只计一个领取用户。</p></Modal>
    <Modal v-if="sourcesOpen" title="选择整个来源" wide @close="sourcesOpen = false"><p class="small muted">按节点顺序填入剩余额度，保存后固定为本次节点快照。默认最多 200 个节点。</p><SourceBrowser :provider-id="filters.provider_id" :following="!!filters.following" :selected="wholeSourceIDs" @choose="selectSource" /><p v-if="selectionError" class="notice warning" role="alert">{{ selectionError }}</p><div class="dialog-actions"><span class="small muted">已选 {{ draftSources.length }} 个整来源</span><button class="primary" @click="sourcesOpen = false">完成选择</button></div></Modal>
    <Modal v-if="reviewOpen" :title="editingSubscription ? '保存组合修改' : '保存组合订阅'" :busy="saving" @close="reviewOpen = false"><form @submit.prevent="save"><label class="form-field"><span class="field-label">订阅名称</span><input v-model="draftName" required maxlength="200" :disabled="saving" /></label><div class="selection-summary"><strong>{{ draftNodes.length }} 个单选节点 · {{ draftSources.length }} 个整来源</strong><p class="small muted">本次涉及 {{ selectedSources.size }} 个来源，最多约 {{ draftEstimate }} 个节点（跨来源去重后可能更少）。</p><p class="small muted">账号已用 {{ profile?.usage.nodes ?? 0 }} / {{ currentUser?.node_limit ?? 200 }} 节点、{{ profile?.usage.sources ?? 0 }} / {{ currentUser?.source_limit ?? 3 }} 来源。保存时按全部订阅合计校验。</p></div><p v-if="draftSources.length" class="notice info">整来源只填入剩余额度，超出的节点会截断，保存后显示实际节点数。</p><ul class="draft-list"><li v-for="source in draftSources" :key="`s${source.id}`"><span>整来源 · {{ source.name }}</span><button type="button" class="icon-button" :disabled="saving" :aria-label="`移除来源 ${source.name}`" @click="toggleSource(source)"><AppIcon name="close" /></button></li><li v-for="node in draftNodes" :key="node.node_id"><span>{{ node.name || `节点 #${node.node_id}` }} <small class="muted">来源 #{{ node.source_id }}</small></span><button type="button" class="icon-button" :disabled="saving" :aria-label="`移除节点 ${node.name || node.node_id}`" @click="toggleNode(node)"><AppIcon name="close" /></button></li></ul><p v-if="saveError" class="notice error" role="alert">{{ saveError }}</p><div class="dialog-actions"><button type="button" :disabled="saving" @click="reviewOpen = false">继续选择</button><button class="primary" :disabled="saving || !draftName.trim() || (!draftNodes.length && !draftSources.length)">{{ saving ? '正在保存…' : '保存订阅' }}</button></div></form></Modal>
  </section>
</template>
