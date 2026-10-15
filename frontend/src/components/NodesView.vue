<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi, type NodePage, type Options, type PoolNode, type Source, type SystemInfo } from '../api/system'
import { usePolling } from '../polling'
import Modal from './Modal.vue'
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
const nodeTesting = reactive(new Set<number>())
const allTesting = ref(false)
const actionError = ref('')
const actionMessage = ref('')
const details = ref<PoolNode | null>(null)
const maxPages = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / pageSize.value)))
const statusLabels = { healthy: '健康', unhealthy: '不可用', unknown: '待检测' }
let debounce: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
let sequence = 0
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
watch(filters, () => { page.value = 1 }, { flush: 'sync' })
watch(pageSize, () => { page.value = 1 }, { flush: 'sync' })
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
const tasks = computed(() => props.system?.tasks)
</script>

<template>
  <section>
    <div class="page-heading"><div><p class="eyebrow">NODE OVERVIEW</p><h1>节点总览</h1><p class="muted">汇集所有来源的唯一节点，持续掌握连接质量。</p></div><button class="primary" :disabled="!data?.stats.total || allTesting || nodeTesting.size > 0 || paused" @click="testAll"><span v-if="allTesting" class="spinner"></span>◉ {{ allTesting ? '正在排队' : '检测全部节点' }}</button></div>
    <div class="stats-grid">
      <article class="stat-card"><span>有效节点</span><strong>{{ data?.stats.total?.toLocaleString() ?? '—' }}</strong><small>去重后的节点总数 <i>◈</i></small></article>
      <article class="stat-card"><span><span class="dot"></span>健康节点</span><strong class="green-text">{{ data?.stats.healthy?.toLocaleString() ?? '—' }}</strong><small>连接状态正常 <i>↗</i></small></article>
      <article class="stat-card"><span><span class="dot bad"></span>不可用</span><strong>{{ data?.stats.unhealthy?.toLocaleString() ?? '—' }}</strong><small>等待后续检测恢复 <i>↘</i></small></article>
      <article class="stat-card"><span><span class="dot pending"></span>待检测</span><strong>{{ data?.stats.pending?.toLocaleString() ?? '—' }}</strong><small>按队列错峰执行 <i>◷</i></small></article>
    </div>
    <div class="section-line"><span><span class="dot"></span>每 6 小时自动检测 · 手动检测同样按队列执行</span><span v-if="tasks">检测中 {{ tasks.active_nodes }} · 排队 {{ tasks.pending_nodes }}</span></div>
    <div v-if="actionError" class="notice error" role="alert">{{ actionError }}</div><div v-if="actionMessage" class="notice success" role="status">{{ actionMessage }}</div>
    <div class="panel node-panel">
      <div class="panel-heading"><div><h2>所有节点 <span class="count">{{ data?.total ?? '—' }}</span></h2><p class="small muted">相同连接配置合并展示，来源关联完整保留。</p></div><button :disabled="loading || paused" @click="load">{{ loading ? '读取中…' : '↻ 更新列表' }}</button></div>
      <div class="filters">
        <label class="search-field"><span class="sr-only">搜索节点</span><span aria-hidden="true">⌕</span><input v-model="filters.q" type="search" placeholder="搜索节点名称或地址" /></label>
        <label><span class="sr-only">协议</span><select v-model="filters.type"><option value="">全部协议</option><option v-for="type in options.types" :key="type" :value="type">{{ type.toUpperCase() }}</option></select></label>
        <label><span class="sr-only">来源</span><select v-model="filters.source_id"><option value="">全部来源</option><option v-for="source in sources" :key="source.id" :value="String(source.id)">{{ source.name }}{{ source.enabled ? '' : '（已停用）' }}</option></select></label>
        <label><span class="sr-only">国家或地区</span><select v-model="filters.country"><option value="">全部国家 / 地区</option><option v-for="country in options.countries" :key="country.code" :value="country.code">{{ country.name || country.code }}</option></select></label>
        <label><span class="sr-only">健康状态</span><select v-model="filters.status"><option value="">全部状态</option><option value="healthy">健康</option><option value="unhealthy">不可用</option><option value="unknown">待检测</option></select></label>
        <label class="latency-filter"><span class="sr-only">最大延迟（毫秒）</span><input v-model="filters.max_latency" type="number" min="0" step="1" placeholder="最大延迟" /><span>ms</span></label>
        <label><span class="sr-only">排序</span><select v-model="filters.sort"><option value="latency">延迟优先</option><option value="name">按名称排序</option><option value="created">按加入时间</option></select></label>
        <button class="text-button" @click="reset">重置</button>
      </div>
      <div v-if="optionsError || sourcesError" class="notice warning small">{{ optionsError ? `筛选选项读取失败：${optionsError}` : `来源选项读取失败：${sourcesError}` }}<button @click="loadOptions(); emit('reloadSources')">重试选项</button></div>
      <div v-if="error" class="notice error" role="alert">{{ error }}<span v-if="data">当前保留上次读取结果。</span><button :disabled="loading" @click="load">重试</button></div>
      <div class="table-wrap" :aria-busy="loading">
        <table v-if="data?.items.length"><thead><tr><th>节点名称 / 地址</th><th>协议</th><th>国家 / 出口 IP</th><th>延迟 / 状态</th><th>关联来源</th><th>最近检测</th><th class="right">操作</th></tr></thead><tbody>
          <tr v-for="node in data.items" :key="node.id">
            <td><button class="node-name text-button" @click="details = node">{{ node.name }}</button><span class="cell-sub mono">{{ node.server }}:{{ node.port }}</span></td>
            <td><span class="protocol-tag">{{ node.type.toUpperCase() }}</span></td>
            <td><span>{{ node.country || node.country_code || '待识别' }}</span><span class="cell-sub mono">{{ node.exit_ip || '—' }}</span></td>
            <td><span :class="['latency', { 'green-text': node.status === 'healthy' }]">{{ node.latency_ms === null ? '—' : `${node.latency_ms} ms` }}</span><span :class="['cell-sub', 'status-text', node.status]"><span class="dot" :class="{ bad: node.status === 'unhealthy', pending: node.status === 'unknown' }"></span>{{ statusLabels[node.status] }}</span></td>
            <td><button class="source-association" @click="details = node">{{ node.sources[0]?.name || '—' }}<span v-if="node.sources.length > 1" class="count">+{{ node.sources.length - 1 }}</span></button></td>
            <td><span class="small">{{ formatDate(node.last_checked_at) }}</span><span class="cell-sub">下次 {{ formatDate(node.next_check_at) }}</span></td>
            <td class="right"><button class="compact" :disabled="nodeTesting.has(node.id) || allTesting || paused" @click="testNode(node)">{{ nodeTesting.has(node.id) ? '排队中…' : '检测' }}</button></td>
          </tr>
        </tbody></table>
        <div v-else-if="loading && !data" class="empty-state" role="status"><span class="spinner"></span><h3>正在读取节点</h3><p>等待服务返回最新列表…</p></div>
        <div v-else-if="!error" class="empty-state"><span class="empty-icon">◈</span><h3>{{ data?.stats.total ? '没有符合条件的节点' : '还没有有效节点' }}</h3><p>{{ data?.stats.total ? '尝试调整搜索词或筛选条件。' : '前往来源管理，导入订阅、本地文件或节点链接。' }}</p><button v-if="data?.stats.total" @click="reset">清空筛选</button></div>
      </div>
      <div class="pagination"><span class="small muted">{{ loading ? '正在更新…' : `共 ${data?.total ?? 0} 个匹配节点` }}</span><div><label class="inline-label">每页 <select v-model.number="pageSize"><option :value="25">25</option><option :value="50">50</option><option :value="100">100</option></select></label><button :disabled="page <= 1 || loading || !data?.total" aria-label="上一页" @click="page--">←</button><span class="small">{{ page }} / {{ maxPages }}</span><button :disabled="page >= maxPages || loading || !data?.total" aria-label="下一页" @click="page++">→</button></div></div>
    </div>
    <Modal v-if="details" title="节点详情" @close="details = null"><div class="detail-title"><h3>{{ details.name }}</h3><span class="protocol-tag">{{ details.type.toUpperCase() }}</span></div><dl class="detail-list"><div><dt>连接地址</dt><dd class="mono">{{ details.server }}:{{ details.port }}</dd></div><div><dt>状态</dt><dd>{{ statusLabels[details.status] }} · {{ details.latency_ms === null ? '暂无延迟' : `${details.latency_ms} ms` }}</dd></div><div><dt>出口</dt><dd>{{ details.country || details.country_code || '待识别' }} · {{ details.exit_ip || '暂无出口 IP' }}</dd></div><div><dt>最近检测</dt><dd>{{ formatDate(details.last_checked_at) }}</dd></div><div><dt>下次检测</dt><dd>{{ formatDate(details.next_check_at) }}</dd></div></dl><p v-if="details.last_error" class="notice error">{{ details.last_error }}</p><h3>关联来源 <span class="count">{{ details.sources.length }}</span></h3><ul class="association-list"><li v-for="source in details.sources" :key="source.id"><strong>{{ source.name }}</strong><span class="small muted">原始名称：{{ source.node_name }}</span></li></ul><p class="small muted">同一连接配置只检测一次，不同来源的原始名称分别保留。</p></Modal>
  </section>
</template>
