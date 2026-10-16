<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi, type NodePage, type Options, type PoolNode, type Source, type SystemInfo } from '../api/system'
import { usePolling } from '../polling'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import CustomSelect from './CustomSelect.vue'
import NodeLocation from './NodeLocation.vue'
import { route, routeParams, setRouteQuery } from '../router'
const props = defineProps<{ sources: Source[]; sourcesError: string; system: SystemInfo | null; paused: boolean }>()
const emit = defineEmits<{ reloadSources: [] }>()
const api = useApi()
const data = ref<NodePage | null>(null)
const loading = ref(false)
const error = ref('')
const options = ref<Options>({ types: [], countries: [] })
const optionsError = ref('')
const filters = reactive({ q: '', type: '', country: '', source_id: '', status: '', max_latency: '', sort: 'latency' })
const page = ref(1)
const pageSize = ref(50)
const layout = ref<'list' | 'cards'>('cards')
const nodeTesting = reactive(new Set<number>())
const allTesting = ref(false)
const actionError = ref('')
const actionMessage = ref('')
const details = ref<PoolNode | null>(null)
const maxPages = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / pageSize.value)))
const statusLabels = { healthy: '健康', unhealthy: '不可用', unknown: '待检测' }
const protocolOptions = computed(() => [{ value: '', label: '全部协议' }, ...options.value.types.map(type => ({ value: type, label: type.toUpperCase() }))])
const sourceOptions = computed(() => [{ value: '', label: '全部来源' }, ...props.sources.map(source => ({ value: String(source.id), label: `${source.name}${source.enabled ? '' : '（已停用）'}` }))])
const countryOptions = computed(() => [{ value: '', label: '全部国家 / 地区' }, ...options.value.countries.map(country => ({ value: country.code, label: country.name || country.code }))])
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
  for (const [key, value] of Object.entries(filters)) {
    const text = String(value).trim()
    if (key === 'sort' || !text) continue
    if (key === 'max_latency') {
      const number = Number(text)
      if (Number.isFinite(number)) params.set(key, String(Math.max(0, Math.floor(number))))
    } else params.set(key, text)
  }
  return params.toString()
})
function pageParams() {
  const params = new URLSearchParams()
  if (page.value !== 1) params.set('page', String(page.value))
  if (pageSize.value !== 50) params.set('page_size', String(pageSize.value))
  if (filters.sort !== 'latency') params.set('sort', filters.sort)
  if (layout.value !== 'cards') params.set('view', layout.value)
  for (const [key, value] of Object.entries(filters)) {
    const text = String(value).trim()
    if (key === 'sort' || !text) continue
    if (key === 'max_latency') {
      const number = Number(text)
      if (Number.isFinite(number) && number > 0) params.set(key, String(Math.max(0, Math.floor(number))))
    } else params.set(key, text)
  }
  return params
}
function restoreRoute() {
  if (route.value.section !== 'nodes') return
  const params = routeParams('nodes')
  restoringRoute = true
  const size = Number(params.get('page_size'))
  pageSize.value = [25, 50, 100].includes(size) ? size : 50
  const requestedPage = Number(params.get('page'))
  page.value = Number.isInteger(requestedPage) && requestedPage > 0 && requestedPage <= 1000000 ? requestedPage : 1
  const country = params.get('country')?.toUpperCase() ?? ''
  const sourceID = Number(params.get('source_id'))
  const latency = Number(params.get('max_latency'))
  Object.assign(filters, {
    q: params.get('q') ?? '', type: params.get('type') ?? '',
    country: /^[A-Z]{2}$/.test(country) ? country : '',
    source_id: Number.isSafeInteger(sourceID) && sourceID > 0 ? String(sourceID) : '',
    status: ['healthy', 'unhealthy', 'unknown'].includes(params.get('status') ?? '') ? params.get('status') : '',
    max_latency: Number.isFinite(latency) && latency > 0 ? String(Math.min(2147483647, Math.floor(latency))) : '',
    sort: ['latency', 'name', 'created'].includes(params.get('sort') ?? '') ? params.get('sort') : 'latency',
  })
  const view = params.get('view')
  layout.value = view === 'list' ? 'list' : 'cards'
  restoringRoute = false
  setRouteQuery('nodes', pageParams(), true)
}
async function load() {
  if (props.paused) return
  const currentSequence = ++sequence
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  try {
    const result = await api<NodePage>(`/nodes?${query.value}`, { signal: controller.signal })
    if (currentSequence !== sequence) return
    if (page.value > Math.max(1, Math.ceil(result.total / pageSize.value))) {
      replaceNextRoute = true
      page.value = Math.max(1, Math.ceil(result.total / pageSize.value))
      return
    }
    data.value = result
    error.value = ''
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
  const before = new URLSearchParams(previous)
  const after = new URLSearchParams(next)
  const typing = before.get('q') !== after.get('q') || before.get('max_latency') !== after.get('max_latency')
  const replace = typing || replaceNextRoute
  replaceNextRoute = false
  setRouteQuery('nodes', pageParams(), replace)
})
watch(route, restoreRoute, { flush: 'sync', immediate: true })
watch(query, () => {
  // Invalidate the visible page immediately so a late response cannot replace newer filters.
  ++sequence
  controller?.abort()
  loading.value = true
  clearTimeout(debounce)
  debounce = setTimeout(() => void load(), 300)
})
watch(() => props.paused, paused => {
  if (paused) { ++sequence; clearTimeout(debounce); controller?.abort(); loading.value = false }
  else { void load(); void loadOptions() }
})
usePolling(async () => { if (!loading.value && !props.paused) await load() }, 5000)
usePolling(async () => { if (!props.paused) await loadOptions() }, 20000)
onBeforeUnmount(() => { ++sequence; clearTimeout(debounce); controller?.abort() })
function reset() { Object.assign(filters, { q: '', type: '', country: '', source_id: '', status: '', max_latency: '', sort: 'latency' }); page.value = 1 }
async function testNode(node: PoolNode) {
  if (nodeTesting.has(node.id) || allTesting.value) return
  nodeTesting.add(node.id)
  actionError.value = ''; actionMessage.value = ''
  try {
    await api(`/nodes/${node.id}/test`, json('POST'))
    actionMessage.value = `「${node.name}」已加入检测队列，结果将自动更新。`
  } catch (cause) { if (!isCancelled(cause)) actionError.value = errorText(cause) }
  finally { nodeTesting.delete(node.id) }
}
async function testAll() {
  if (allTesting.value || !data.value?.stats.total) return
  allTesting.value = true
  actionError.value = ''; actionMessage.value = ''
  try {
    await api('/nodes/test', json('POST', {}))
    actionMessage.value = '全部有效节点已加入检测队列，结果将自动更新。'
  } catch (cause) { if (!isCancelled(cause)) actionError.value = errorText(cause) }
  finally { allTesting.value = false }
}
</script>

<template>
  <section>
    <div class="page-heading"><div><p class="eyebrow">NODE OVERVIEW</p><h1>节点总览</h1><p class="muted">汇集所有来源的唯一节点，持续掌握连接质量。</p></div><button class="primary" :disabled="!data?.stats.total || allTesting || nodeTesting.size > 0 || paused" @click="testAll"><span v-if="allTesting" class="spinner"></span><AppIcon name="probe" /><span>{{ allTesting ? '正在排队' : '检测全部节点' }}</span></button></div>
    <div class="stats-grid">
      <article class="stat-card"><span><AppIcon name="nodes" />有效节点</span><strong>{{ data?.stats.total?.toLocaleString() ?? '—' }}</strong></article>
      <article class="stat-card"><span><span class="dot"></span>健康节点</span><strong class="green-text">{{ data?.stats.healthy?.toLocaleString() ?? '—' }}</strong></article>
      <article class="stat-card"><span><span class="dot bad"></span>不可用</span><strong>{{ data?.stats.unhealthy?.toLocaleString() ?? '—' }}</strong></article>
      <article class="stat-card"><span><span class="dot pending"></span>待检测</span><strong>{{ data?.stats.pending?.toLocaleString() ?? '—' }}</strong></article>
    </div>
    <div v-if="actionError" class="notice error" role="alert">{{ actionError }}</div><div v-if="actionMessage" class="notice success" role="status">{{ actionMessage }}</div>
    <div class="panel node-panel">
      <div class="panel-heading node-toolbar"><div><h2>所有节点 <span class="count">{{ data?.total ?? '—' }}</span></h2></div><div class="node-toolbar-actions"><div class="view-switch" role="group" aria-label="节点展示方式"><button type="button" :aria-pressed="layout === 'cards'" aria-label="卡片视图" title="卡片视图" @click="layout = 'cards'"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M2 2h4v4H2zm8 0h4v4h-4zM2 10h4v4H2zm8 0h4v4h-4z" /></svg></button><button type="button" :aria-pressed="layout === 'list'" aria-label="列表视图" title="列表视图" @click="layout = 'list'"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M2 3h2m2 0h8M2 8h2m2 0h8M2 13h2m2 0h8" /></svg></button></div><button type="button" class="icon-button" aria-label="刷新节点" title="刷新节点" :disabled="loading || paused" @click="load"><span v-if="loading" class="spinner"></span><AppIcon v-else name="refresh" /></button></div></div>
      <div class="filters">
        <label class="search-field"><span class="sr-only">搜索节点</span><svg aria-hidden="true" viewBox="0 0 20 20"><circle cx="8.5" cy="8.5" r="5.5" /><path d="m13 13 4 4" /></svg><input v-model="filters.q" type="search" placeholder="搜索节点名称或地址" /></label>
        <label class="latency-filter"><span class="sr-only">最大延迟（毫秒）</span><input v-model="filters.max_latency" type="number" min="0" step="1" placeholder="最大延迟" /><span>ms</span></label>
        <CustomSelect v-model="filters.type" label="协议" :options="protocolOptions" />
        <CustomSelect v-model="filters.source_id" label="来源" :options="sourceOptions" />
        <CustomSelect v-model="filters.country" label="国家或地区" :options="countryOptions" />
        <CustomSelect v-model="filters.status" label="健康状态" :options="healthOptions" />
        <CustomSelect v-model="filters.sort" label="排序" :options="sortOptions" />
        <button class="text-button" @click="reset">重置</button>
      </div>
      <div v-if="optionsError || sourcesError" class="notice warning small">{{ optionsError ? `筛选选项读取失败：${optionsError}` : `来源选项读取失败：${sourcesError}` }}<button @click="loadOptions(); emit('reloadSources')">重试选项</button></div>
      <div v-if="error" class="notice error" role="alert">{{ error }}<span v-if="data">当前保留上次读取结果。</span><button :disabled="loading" @click="load">重试</button></div>
      <div :class="layout === 'list' ? 'table-wrap' : 'node-cards-wrap'" :aria-busy="loading">
        <table v-if="data?.items.length && layout === 'list'" class="node-table"><thead><tr><th>节点名称 / 地址</th><th>协议</th><th>出口 IP / 地区</th><th>延迟 / 状态</th><th>关联来源</th><th>最近检测</th><th class="right">操作</th></tr></thead><tbody>
          <tr v-for="node in data.items" :key="node.id"><td><button class="node-name text-button" @click="details = node">{{ node.name }}</button><span class="cell-sub mono">{{ node.server }}:{{ node.port }}</span></td><td><span class="protocol-tag">{{ node.type.toUpperCase() }}</span></td><td><NodeLocation :node="node" /></td><td><span :class="['latency', { 'green-text': node.status === 'healthy' }]">{{ node.latency_ms === null ? '—' : `${node.latency_ms} ms` }}</span><span :class="['cell-sub', 'status-text', node.status]"><span class="dot" :class="{ bad: node.status === 'unhealthy', pending: node.status === 'unknown' }"></span>{{ statusLabels[node.status] }}</span></td><td><button class="source-association" @click="details = node">{{ node.sources[0]?.name || '—' }}<span v-if="node.sources.length > 1" class="count">+{{ node.sources.length - 1 }}</span></button></td><td><span class="small">{{ formatDate(node.last_checked_at) }}</span></td><td class="right"><button class="compact" :disabled="nodeTesting.has(node.id) || allTesting || paused" @click="testNode(node)"><AppIcon name="probe" /><span>{{ nodeTesting.has(node.id) ? '排队中…' : '检测' }}</span></button></td></tr>
        </tbody></table>
        <div v-else-if="data?.items.length" class="node-cards"><article v-for="node in data.items" :key="node.id" class="node-card" :data-node-id="node.id"><div class="node-card-heading"><button class="node-name text-button" @click="details = node">{{ node.name }}</button><span class="protocol-tag">{{ node.type.toUpperCase() }}</span></div><p class="node-card-address mono">{{ node.server }}:{{ node.port }}</p><NodeLocation :node="node" /><div class="node-card-health"><span :class="['latency', { 'green-text': node.status === 'healthy' }]">{{ node.latency_ms === null ? '—' : `${node.latency_ms} ms` }}</span><span class="status-text"><span class="dot" :class="{ bad: node.status === 'unhealthy', pending: node.status === 'unknown' }"></span>{{ statusLabels[node.status] }}</span></div><div class="node-card-source"><span class="small muted">关联来源</span><button class="source-association" @click="details = node">{{ node.sources[0]?.name || '—' }}<span v-if="node.sources.length > 1" class="count">+{{ node.sources.length - 1 }}</span></button></div><div class="node-card-actions"><button class="compact" :aria-label="`查看 ${node.name} 的详情`" @click="details = node">详情</button><button class="compact" :disabled="nodeTesting.has(node.id) || allTesting || paused" :aria-label="`检测 ${node.name}`" @click="testNode(node)"><AppIcon name="probe" /><span>{{ nodeTesting.has(node.id) ? '排队中…' : '检测' }}</span></button></div></article></div>
        <div v-else-if="loading && !data" class="empty-state" role="status"><span class="spinner"></span><h3>正在读取节点</h3><p>等待服务返回最新列表…</p></div>
        <div v-else-if="!error" class="empty-state"><span class="empty-icon"><AppIcon name="nodes" /></span><h3>{{ data?.stats.total ? '没有符合条件的节点' : '还没有有效节点' }}</h3><p>{{ data?.stats.total ? '尝试调整搜索词或筛选条件。' : '前往来源管理，导入订阅、本地文件或节点链接。' }}</p><button v-if="data?.stats.total" @click="reset">清空筛选</button></div>
      </div>
      <div class="pagination"><span class="small muted">{{ loading ? '正在更新…' : `共 ${data?.total ?? 0} 个匹配节点` }}</span><div><div class="inline-label"><span>每页</span><CustomSelect v-model="pageSize" label="每页条数" :options="sizeOptions" /></div><button :disabled="page <= 1 || loading || !data?.total" aria-label="上一页" title="上一页" @click="page--"><AppIcon name="left" /></button><span class="small">{{ page }} / {{ maxPages }}</span><button :disabled="page >= maxPages || loading || !data?.total" aria-label="下一页" title="下一页" @click="page++"><AppIcon name="right" /></button></div></div>
    </div>
    <Modal v-if="details" title="节点详情" @close="details = null"><div class="detail-title"><h3>{{ details.name }}</h3><span class="protocol-tag">{{ details.type.toUpperCase() }}</span></div><dl class="detail-list"><div><dt>连接地址</dt><dd class="mono">{{ details.server }}:{{ details.port }}</dd></div><div><dt>状态</dt><dd>{{ statusLabels[details.status] }} · {{ details.latency_ms === null ? '暂无延迟' : `${details.latency_ms} ms` }}</dd></div><div><dt>出口 / 地区</dt><dd><NodeLocation :node="details" /></dd></div><div><dt>最近检测</dt><dd>{{ formatDate(details.last_checked_at) }}</dd></div></dl><p v-if="details.last_error" class="notice error">{{ details.last_error }}</p><h3>关联来源 <span class="count">{{ details.sources.length }}</span></h3><ul class="association-list"><li v-for="source in details.sources" :key="source.id"><strong>{{ source.name }}</strong><span class="small muted">原始名称：{{ source.node_name }}</span></li></ul></Modal>
  </section>
</template>
