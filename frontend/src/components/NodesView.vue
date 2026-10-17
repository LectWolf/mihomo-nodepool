<script setup lang="ts">
import { toast } from '../toast'
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi, type NodePage, type Options, type PoolNode, type Source } from '../api/system'
import { currentUser, isAdmin, isGuest, requireLogin, profile, draftNodes, draftSources, draftName, draftRuleProfile, selectingNodes, draftEstimate, selectedSources, limitedSources, editingSubscription, selectionError, selectionNotice, toggleNode, toggleSource, resetDraft, saveDraft, loadProfile, type Subscription, limits } from '../api/accounts'
import { usePolling } from '../polling'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import TitleChip from './TitleChip.vue'
import PageNumbers from './PageNumbers.vue'
import CustomSelect from './CustomSelect.vue'
import NodeLocation from './NodeLocation.vue'
import CountryFilter from './CountryFilter.vue'
import FilterPopover from './FilterPopover.vue'
import SourceBrowser from './SourceBrowser.vue'
import DraftPreview from './DraftPreview.vue'
import RuleProfileSelect from './RuleProfileSelect.vue'
import NodeBadges from './NodeBadges.vue'
import UsageHint from './UsageHint.vue'
import UserAvatar from './UserAvatar.vue'
import FeedbackPanel from './FeedbackPanel.vue'
import FeedbackBadge from './FeedbackBadge.vue'
import FieldTestBadge from './FieldTestBadge.vue'
import SourceGallery from './SourceGallery.vue'
import { blockHint, blockLabel, communityUser, levelOptions, selectionFor, useAccountScope } from '../community'
import '../community.css'
import { navigate, route, routeParams, setRouteQuery } from '../router'
import { cached, remember } from '../view-cache'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const minimumOptions = [{ value: '', label: '全部等级门槛' }, ...levelOptions.map(option => ({ value: String(option.value), label: option.label }))]
const data = ref<NodePage | null>(null)
const loading = ref(false)
const error = ref('')
const options = ref<Options>({ types: [], countries: [] })
const optionsError = ref('')
const filters = reactive({ q: '', type: '', countries: '', source_id: '', status: '', provider_id: '', following: '', min_level: '', available: '', sort: 'recommend' })
// 按节点或按来源浏览
const browse = ref<'nodes' | 'sources'>('nodes')
const countries = computed({ get: () => filters.countries ? filters.countries.split(',') : [], set: (value: string[]) => { filters.countries = value.join(',') } })
const sourceName = ref('')
const sourceText = computed(() => filters.source_id ? sourceName.value || `来源 #${filters.source_id}` : '全部来源')
const page = ref(1)
const pageSize = ref(50)
const layout = ref<'list' | 'cards'>('cards')
const nodeTesting = reactive(new Set<number>())
const actionError = ref('')
const details = ref<PoolNode | null>(null)
const originNode = ref<PoolNode | null>(null)
const sourcesOpen = ref(false)
const reviewOpen = ref(false)
const saving = ref(false)
const saveError = ref('')
const existingSubscriptions = ref<Subscription[]>([])
const reviewLoading = ref(false)
const reviewFailed = ref(false)
const saveTarget = computed({ get: () => editingSubscription.value ?? '', set: (value: string | number) => { editingSubscription.value = value === '' ? null : Number(value) } })
const targetOptions = computed(() => [{ value: '' as string | number, label: '新建组合订阅' }, ...existingSubscriptions.value.map(item => ({ value: item.id, label: `覆盖 · ${item.name}` }))])
const subscriptionFull = computed(() => !editingSubscription.value && existingSubscriptions.value.length >= (limits.value.subscriptions))
const selectedIDs = computed(() => new Set(draftNodes.value.map(n => n.node_id)))
const wholeSourceIDs = computed(() => draftSources.value.map(s => s.id))
const hasDraft = computed(() => draftNodes.value.length > 0 || draftSources.value.length > 0 || editingSubscription.value !== null)
const maxPages = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / pageSize.value)))
const statusLabels = { healthy: '健康', unhealthy: '不可用', unknown: '待检测' }
const protocolOptions = computed(() => [{ value: '', label: '全部协议' }, ...options.value.types.map(type => ({ value: type, label: type.toUpperCase() }))])
const healthOptions = [{ value: '', label: '全部状态' }, { value: 'healthy', label: '健康' }, { value: 'unhealthy', label: '不可用' }, { value: 'unknown', label: '待检测' }]
const sortOptions = [{ value: 'recommend', label: '推荐' }, { value: 'latency', label: '延迟优先' }, { value: 'name', label: '按名称排序' }, { value: 'created', label: '按加入时间' }]
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
  if (filters.sort !== 'recommend') params.set('sort', filters.sort)
  if (layout.value !== 'cards') params.set('view', layout.value)
  if (browse.value === 'sources') params.set('by', 'sources')
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
    min_level: /^[0-6]$/.test(params.get('min_level') ?? '') ? params.get('min_level') : '',
    status: ['healthy', 'unhealthy', 'unknown'].includes(params.get('status') ?? '') ? params.get('status') : '',
    available: params.get('available') === '1' ? '1' : '',
    sort: ['recommend', 'latency', 'name', 'created'].includes(params.get('sort') ?? '') ? params.get('sort') : 'recommend',
  })
  layout.value = params.get('view') === 'list' ? 'list' : 'cards'
  browse.value = params.get('by') === 'sources' ? 'sources' : 'nodes'
  restoringRoute = false; setRouteQuery('nodes', pageParams(), true)
}
// 手机上筛选项默认折叠
const filtersOpen = ref(false)
const activeFilters = computed(() => [filters.type, filters.source_id, filters.min_level, filters.status].filter(Boolean).length + (countries.value.length ? 1 : 0))
async function load() {
  if (props.paused) return
  const ticket = scope.ticket()
  const currentSequence = ++sequence
  controller?.abort(); controller = new AbortController(); loading.value = true
  try {
    const result = await api<NodePage>(`/nodes?${query.value}`, { signal: controller.signal })
    if (currentSequence !== sequence || !scope.current(ticket)) return
    const last = Math.max(1, Math.ceil(result.total / pageSize.value))
    if (page.value > last) { replaceNextRoute = true; page.value = last; return }
    data.value = result; error.value = ''; remember(`/nodes?${query.value}`, result)
    if (details.value) details.value = result.items.find(item => item.id === details.value?.id) ?? details.value
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause) && currentSequence === sequence) error.value = errorText(cause) }
  finally { if (currentSequence === sequence) loading.value = false }
}
async function loadOptions() {
  const ticket = scope.ticket()
  try { const value = await api<Options>('/options'); if (scope.current(ticket)) { options.value = value; optionsError.value = ''; remember('/options', value) } }
  catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) optionsError.value = errorText(cause) }
}
watch(filters, () => { if (!restoringRoute) page.value = 1 }, { flush: 'sync' })
watch(pageSize, () => { if (!restoringRoute) page.value = 1 }, { flush: 'sync' })
watch([query, layout, browse], ([next], [previous]) => {
  if (restoringRoute || route.value.section !== 'nodes') return
  const typing = new URLSearchParams(previous).get('q') !== new URLSearchParams(next).get('q')
  setRouteQuery('nodes', pageParams(), typing || replaceNextRoute); replaceNextRoute = false
})
watch(route, restoreRoute, { flush: 'sync', immediate: true })
data.value = cached<NodePage>(`/nodes?${query.value}`) ?? null
options.value = cached<Options>('/options') ?? options.value
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
function address(node: PoolNode) { return node.server_hidden ? '地址仅提供方可见' : `${node.server}:${node.port}` }
function ownsAll(node: PoolNode) { return node.sources.every(s => s.owner_id === currentUser.value?.id) }
function viewProvider(id: number) { navigate('nodes', new URLSearchParams({ provider_id: String(id) })); details.value = null }
// 健康节点按延迟分三档着色
function latencyTier(node: PoolNode) { if (node.status !== 'healthy' || node.latency_ms == null) return ''; return node.latency_ms < 120 ? 'lat-fast' : node.latency_ms < 250 ? 'lat-mid' : 'lat-slow' }
function reset() { Object.assign(filters, { q: '', type: '', countries: '', source_id: '', status: '', provider_id: '', following: '', min_level: '', available: '', sort: 'recommend' }); sourceName.value = ''; page.value = 1 }
function chooseFilter(source: Source, close: () => void) { sourceName.value = source.name; filters.source_id = String(source.id); close() }
async function testNode(node: PoolNode) {
  if (props.paused || !isAdmin.value || nodeTesting.has(node.id)) return
  const ticket = scope.ticket()
  nodeTesting.add(node.id); actionError.value = ''
  try { await api(`/nodes/${node.id}/test`, json('POST')); if (scope.current(ticket)) toast(`「${node.name}」已加入检测队列，结果将自动更新`, 'info') }
  catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) actionError.value = errorText(cause) }
  finally { nodeTesting.delete(node.id) }
}
function origins(node: PoolNode) {
  const scoped = node.sources.filter(s => (!filters.source_id || s.id === Number(filters.source_id)) && (!filters.provider_id || s.owner_id === Number(filters.provider_id)) && (filters.min_level === '' || (s.min_level ?? 0) === Number(filters.min_level)))
  return scoped.length ? scoped : node.sources
}
function originAllowed(source: { owner_id?: number; min_level?: number; unavailable?: string }) { return !source.unavailable && (isAdmin.value || source.owner_id === currentUser.value?.id || communityUser.value.level >= (source.min_level ?? 0)) }
// 新上架来源与按来源视图共用：点“查看节点”切回节点视图并筛选该来源
function viewSource(source: Source) { sourceName.value = source.name; filters.source_id = String(source.id); browse.value = 'nodes' }
const showSpotlight = computed(() => browse.value === 'nodes' && page.value === 1 && !filters.q && !filters.source_id && !filters.provider_id && !filters.following && !filters.type && !filters.countries && !filters.status && filters.min_level === '')
function cardOrigin(node: PoolNode) { return origins(node)[0] }
function chooseNode(node: PoolNode) {
  const existing = draftNodes.value.find(n => n.node_id === node.id)
  if (existing) { toggleNode(existing); return }
  const sources = origins(node)
  // 多来源节点优先不限席位的来源，其次沿用本次已选的有限来源，与后端保存时的选择一致
  const allowed = node.sources.filter(originAllowed)
  const free = allowed.length > 1 ? allowed.find(s => !s.max_users) : undefined
  const reuse = !free && allowed.length > 1 ? allowed.find(s => limitedSources.value.has(s.id)) : undefined
  const auto = free ?? reuse
  if (auto) {
    toggleNode(selectionFor(node, auto))
    if (!selectionError.value) selectionNotice.value = free ? `「${node.name}」由 ${allowed.length} 个来源提供，已自动选择不限席位的「${auto.name}」，不占来源额度。` : `「${node.name}」由 ${allowed.length} 个来源提供，已沿用本次已选的「${auto.name}」，不额外占用来源额度。`
    return
  }
  if (sources.length > 1) { originNode.value = node; return }
  if (sources[0] && originAllowed(sources[0])) toggleNode(selectionFor(node, sources[0]))
  else selectionError.value = blockHint(sources[0]?.unavailable || 'level', sources[0]?.min_level)
}
function chooseOrigin(sourceID: number) {
  const node = originNode.value; if (!node) return
  const source = node.sources.find(source => source.id === sourceID)
  if (!source || !originAllowed(source)) return
  toggleNode(selectionFor(node, source)); originNode.value = null
}
function selectSource(source: Source) {
  if (!draftSources.value.some(item => item.id === source.id) && !originAllowed(source)) { selectionError.value = blockHint(source.unavailable || 'level', source.min_level); return }
  toggleSource(source)
}
function toggleSelecting() { if (!selectingNodes.value && requireLogin('登录后即可挑选节点、组合自己的订阅。')) return; selectingNodes.value = !selectingNodes.value }
function openSources() { if (requireLogin('登录后即可跟随整个来源。')) return; sourcesOpen.value = true }
function activateNode(node: PoolNode) { if (selectingNodes.value) chooseNode(node); else details.value = node }
async function openReview() {
  if (props.paused || reviewLoading.value) return
  const ticket = scope.ticket()
  reviewOpen.value = true; reviewLoading.value = true; reviewFailed.value = false; saveError.value = ''
  try {
    const result = await api<{ items: Subscription[] }>('/subscriptions')
    if (!scope.current(ticket)) return
    existingSubscriptions.value = result.items
    await loadProfile()
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) { saveError.value = errorText(cause); reviewFailed.value = true } }
  finally { reviewLoading.value = false }
}
async function save() {
  if (props.paused || saving.value || reviewLoading.value || reviewFailed.value || subscriptionFull.value || !draftName.value.trim()) return
  const ticket = scope.ticket()
  saving.value = true; saveError.value = ''
  try {
    const result = await saveDraft()
    if (!scope.current(ticket)) return
    toast(result.truncated ? `组合订阅已保存，单选节点超出额度已截取，共 ${result.selected_count} 个节点` : `组合订阅已保存，共 ${result.selected_count} 个节点`, 'success', { action: { label: '我的订阅', run: () => navigate('subscriptions') } })
    resetDraft(); reviewOpen.value = false; selectingNodes.value = false
    try { await loadProfile() } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) actionError.value = `订阅已保存，用量读取失败：${errorText(cause)}` }
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) saveError.value = errorText(cause) }
  finally { saving.value = false }
}
</script>

<template>
  <section>
    <div class="page-heading"><div><p class="eyebrow">NODE OVERVIEW</p><h1>节点总览</h1><p class="muted">{{ selectingNodes ? '点击卡片选择节点，选区会跨页保留。' : '点击卡片查看详情，进入选择模式组合你的订阅。' }}</p></div><button data-tour="select-nodes" :class="{ primary: !selectingNodes }" :aria-pressed="selectingNodes" @click="toggleSelecting"><AppIcon :name="selectingNodes ? 'check' : 'plus'" />{{ selectingNodes ? '完成选择' : '选择节点' }}</button></div>
    <div class="stats-grid">
      <article class="stat-card metric tone-blue"><span><i class="metric-icon"><AppIcon name="nodes" /></i>有效节点</span><strong>{{ data?.stats.total?.toLocaleString() ?? '—' }}</strong></article>
      <article class="stat-card metric tone-green"><span><i class="metric-icon"><AppIcon name="check" /></i>健康节点</span><strong class="green-text">{{ data?.stats.healthy?.toLocaleString() ?? '—' }}</strong></article>
      <article class="stat-card metric tone-red"><span><i class="metric-icon"><AppIcon name="alert" /></i>不可用</span><strong :class="{ 'has-value': (data?.stats.unhealthy ?? 0) > 0 }">{{ data?.stats.unhealthy?.toLocaleString() ?? '—' }}</strong></article>
      <article class="stat-card metric tone-amber"><span><i class="metric-icon"><AppIcon name="clock" /></i>待检测</span><strong>{{ data?.stats.pending?.toLocaleString() ?? '—' }}</strong></article>
    </div>
    <div class="directory-toolbar node-scope">
      <div class="scope-tabs" role="group" aria-label="节点范围"><button :aria-pressed="!filters.following && !filters.provider_id" @click="filters.following = ''; filters.provider_id = ''; filters.source_id = ''">全部节点</button><button v-if="!isGuest" :aria-pressed="!!filters.following" @click="filters.following = filters.following ? '' : '1'; filters.provider_id = ''; filters.source_id = ''"><AppIcon name="heart" />已关注</button><span v-if="filters.provider_id" class="provider-scope">提供方 #{{ filters.provider_id }}<button class="icon-button" aria-label="取消提供方筛选" @click="filters.provider_id = ''; filters.source_id = ''"><AppIcon name="close" /></button></span></div>
      <div class="node-scope-actions"><label v-if="!isAdmin" class="switch-chip" title="隐藏席位已满、等级不足等暂时无法选择的节点"><input v-model="filters.available" type="checkbox" class="switch" true-value="1" false-value="" />只看可选</label><div class="scope-tabs browse-switch" role="group" aria-label="浏览方式"><button type="button" :aria-pressed="browse === 'nodes'" @click="browse = 'nodes'"><AppIcon name="nodes" />按节点</button><button type="button" :aria-pressed="browse === 'sources'" @click="browse = 'sources'"><AppIcon name="sources" />按来源</button></div><button v-if="browse === 'nodes'" @click="openSources"><AppIcon name="plus" />选择整个来源</button></div>
    </div>
    <div v-if="hasDraft" class="selection-bar"><div><strong>{{ editingSubscription ? '编辑组合' : '已选节点' }}</strong><span>{{ draftNodes.length }} 个单选 · 跟随 {{ draftSources.length }} 个来源</span><small class="muted">账号额度 {{ limits.nodes }} 节点 / {{ limits.sources }} 来源（不限人数的来源不占）</small></div><button class="text-button" @click="resetDraft">清空</button><button class="primary" @click="openReview">{{ editingSubscription ? '保存修改' : '保存组合' }}<AppIcon name="right" /></button></div>
    <p v-if="selectionError" class="notice warning" role="alert">{{ selectionError }}</p>
    <p v-if="selectionNotice" class="notice info" role="status">{{ selectionNotice }}</p>
    <div v-if="actionError" class="notice error" role="alert">{{ actionError }}</div>
    <SourceGallery v-if="browse === 'sources'" :paused="paused" :provider-id="filters.provider_id" :following="!!filters.following" @view="viewSource" />
    <SourceGallery v-if="showSpotlight" compact :paused="paused" @view="viewSource" />
    <div v-show="browse === 'nodes'" class="panel node-panel">
      <div class="panel-heading node-toolbar"><h2>所有节点 <span class="count">{{ data?.total ?? '—' }}</span></h2><div class="node-toolbar-actions"><div class="view-switch" role="group" aria-label="节点展示方式"><button type="button" :aria-pressed="layout === 'cards'" aria-label="卡片视图" title="卡片视图" @click="layout = 'cards'"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M2 2h4v4H2zm8 0h4v4h-4zM2 10h4v4H2zm8 0h4v4h-4z" /></svg></button><button type="button" :aria-pressed="layout === 'list'" aria-label="列表视图" title="列表视图" @click="layout = 'list'"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M2 3h2m2 0h8M2 8h2m2 0h8M2 13h2m2 0h8" /></svg></button></div><button type="button" class="icon-button" aria-label="刷新节点" title="刷新节点" :disabled="loading || paused" @click="load"><span v-if="loading" class="spinner"></span><AppIcon v-else name="refresh" /></button></div></div>
      <div v-if="!paused" :class="['filters', 'node-filters', { open: filtersOpen }]">
        <label class="search-field"><span class="sr-only">搜索节点</span><AppIcon name="search" /><input v-model="filters.q" type="search" :placeholder="isAdmin ? '搜索名称或地址' : '搜索节点名称'" /></label>
        <button type="button" class="mobile-filter-toggle" :aria-expanded="filtersOpen" :aria-label="filtersOpen ? '收起筛选' : '展开筛选'" @click="filtersOpen = !filtersOpen"><AppIcon name="filter" />{{ filtersOpen ? "收起筛选" : "筛选" }}<span v-if="activeFilters" class="count">{{ activeFilters }}</span></button>
        <CustomSelect v-model="filters.type" label="协议" :options="protocolOptions" />
        <FilterPopover label="来源" :text="sourceText" :active="!!filters.source_id"><template #default="{ close }"><SourceBrowser mode="filter" :provider-id="filters.provider_id" :following="!!filters.following" :selected="filters.source_id ? [Number(filters.source_id)] : []" @choose="chooseFilter($event, close)" @clear="filters.source_id = ''; sourceName = ''; close()" /></template></FilterPopover>
        <CountryFilter v-model="countries" :options="options.countries" />
        <div class="node-level-filter"><span class="sr-only">来源门槛</span><CustomSelect v-model="filters.min_level" label="来源最低等级筛选" :options="minimumOptions" :disabled="paused" /></div>
        <CustomSelect v-model="filters.status" label="健康状态" :options="healthOptions" />
        <CustomSelect v-model="filters.sort" label="排序" :options="sortOptions" />
        <button type="button" class="icon-button" aria-label="重置筛选" title="重置筛选" @click="reset"><AppIcon name="reset" /></button>
      </div>
      <div v-if="optionsError" class="notice warning small">筛选选项读取失败：{{ optionsError }}<button @click="loadOptions">重试选项</button></div>
      <div v-if="error" class="notice error" role="alert">{{ error }}<span v-if="data">当前保留上次读取结果。</span><button :disabled="loading" @click="load">重试</button></div>
      <div :class="layout === 'list' ? 'table-wrap' : 'node-cards-wrap'" :aria-busy="loading">
        <table v-if="data?.items.length && layout === 'list'" class="node-table"><thead><tr><th v-if="selectingNodes"><span class="sr-only">选择状态</span></th><th>节点名称 / 地址</th><th>协议</th><th>出口 IP / 地区</th><th>延迟 / 状态</th><th>关联来源</th><th>最近检测</th></tr></thead><tbody>
          <tr v-for="node in data.items" :key="node.id" class="node-row" :class="{ selected: selectedIDs.has(node.id), unavailable: !!node.unavailable && !selectedIDs.has(node.id) }" :data-node-id="node.id" @click="activateNode(node)">
            <td v-if="selectingNodes"><span class="selection-indicator" :class="{ on: selectedIDs.has(node.id) }" aria-hidden="true"><AppIcon v-if="selectedIDs.has(node.id)" name="check" /></span></td>
            <td><button class="node-name text-button" :title="node.name" :aria-label="`${selectingNodes ? '选择' : '查看'} ${node.name}`" :aria-pressed="selectingNodes ? selectedIDs.has(node.id) : undefined" @click.stop="activateNode(node)">{{ node.name }}</button><span :class="['cell-sub', { mono: !node.server_hidden, 'address-hidden': node.server_hidden }]">{{ address(node) }}</span><span v-if="node.unavailable" class="mini-tag tone-red block-tag">{{ blockLabel(node.unavailable, cardOrigin(node)?.min_level) }}</span><span v-else-if="node.fresh" class="mini-tag tone-violet">新上架</span><NodeBadges :insights="node.insights" :min-level="cardOrigin(node)?.min_level" :usage="cardOrigin(node)?.subscription_usage" /><FeedbackBadge :summary="node.feedback" /><FieldTestBadge :result="node.field_test" /></td><td><span class="protocol-tag">{{ node.type.toUpperCase() }}</span></td><td><NodeLocation :node="node" /></td>
            <td><span :class="['latency', latencyTier(node), { 'green-text': node.status === 'healthy' }]">{{ node.latency_ms === null ? '—' : `${node.latency_ms} ms` }}</span><span :class="['cell-sub', 'status-text', node.status]"><span class="dot" :class="{ bad: node.status === 'unhealthy', pending: node.status === 'unknown' }"></span>{{ statusLabels[node.status] }}</span></td><td><span class="source-association">{{ node.sources[0]?.name || '—' }}<span v-if="node.sources.length > 1" class="count">+{{ node.sources.length - 1 }}</span></span></td><td><span class="small">{{ formatDate(node.last_checked_at) }}</span></td>
          </tr>
        </tbody></table>
        <div v-else-if="data?.items.length" class="node-cards"><button v-for="node in data.items" :key="node.id" type="button" class="node-card" :class="{ selected: selectedIDs.has(node.id), 'selection-mode': selectingNodes, unavailable: !!node.unavailable && !selectedIDs.has(node.id) }" :title="node.unavailable ? blockHint(node.unavailable, cardOrigin(node)?.min_level) : undefined" :data-node-id="node.id" :data-status="node.status" :aria-label="`${selectingNodes ? '选择' : '查看'} ${node.name}`" :aria-pressed="selectingNodes ? selectedIDs.has(node.id) : undefined" @click="activateNode(node)">
          <span class="node-card-heading"><span v-if="selectingNodes" class="selection-indicator" :class="{ on: selectedIDs.has(node.id) }" aria-hidden="true"><AppIcon v-if="selectedIDs.has(node.id)" name="check" /></span><span class="node-name" :title="node.name">{{ node.name }}</span><span v-if="node.unavailable" class="mini-tag tone-red block-tag">{{ blockLabel(node.unavailable, cardOrigin(node)?.min_level) }}</span><span v-else-if="node.fresh" class="mini-tag tone-violet">新上架</span><span class="protocol-tag">{{ node.type.toUpperCase() }}</span></span>
          <span :class="['node-card-address', { mono: !node.server_hidden, 'address-hidden': node.server_hidden }]" :title="address(node)">{{ address(node) }}</span>
          <span class="node-card-overview"><NodeLocation :node="node" /><span :class="['latency', latencyTier(node), { 'green-text': node.status === 'healthy' }]">{{ node.latency_ms === null ? '—' : `${node.latency_ms} ms` }}</span></span>
          <NodeBadges :insights="node.insights" :min-level="cardOrigin(node)?.min_level" :usage="cardOrigin(node)?.subscription_usage" />
          <span class="node-card-signals"><FeedbackBadge :summary="node.feedback" /><FieldTestBadge :result="node.field_test" /></span>
          <span class="node-card-footer"><span class="source-association" :title="node.sources.map(s => s.name).join(' · ')">{{ node.sources[0]?.name || '—' }}<span v-if="node.sources.length > 1" class="count">+{{ node.sources.length - 1 }}</span></span><span class="status-text"><span class="dot" :class="{ bad: node.status === 'unhealthy', pending: node.status === 'unknown' }"></span>{{ statusLabels[node.status] }}</span></span>
        </button></div>
        <div v-else-if="loading && !data" class="empty-state" role="status"><span class="spinner"></span><h3>正在读取节点</h3><p>等待服务返回最新列表…</p></div>
        <div v-else-if="!error" class="empty-state"><span class="empty-icon"><AppIcon name="nodes" /></span><h3>{{ data?.stats.total ? '没有符合条件的节点' : '还没有有效节点' }}</h3><p>{{ data?.stats.total ? '尝试调整搜索词或筛选条件。' : '前往共享来源，导入订阅、本地文件或节点链接。' }}</p><button v-if="data?.stats.total" @click="reset">清空筛选</button></div>
      </div>
      <div class="pagination"><span class="small muted">{{ loading ? '正在更新…' : `共 ${data?.total ?? 0} 个匹配节点` }}</span><div class="pagination-controls"><div class="inline-label"><span>每页</span><CustomSelect v-model="pageSize" label="每页条数" :options="sizeOptions" /></div><PageNumbers v-model="page" :pages="maxPages" :disabled="loading || !data?.total" /></div></div>
    </div>
    <Modal v-if="details" title="节点详情" @close="details = null">
      <div class="detail-title"><h3>{{ details.name }}</h3><span class="protocol-tag">{{ details.type.toUpperCase() }}</span></div>
      <dl class="detail-list"><div><dt>连接地址</dt><dd :class="{ mono: !details.server_hidden, muted: details.server_hidden }">{{ address(details) }}<small v-if="details.server_hidden" class="field-hint">为防止节点被单独提取，仅提供方与管理员可见；加入订阅后由客户端直接使用。</small></dd></div><div><dt>状态</dt><dd>{{ statusLabels[details.status] }} · {{ details.latency_ms === null ? '暂无延迟' : `${details.latency_ms} ms` }}</dd></div><div><dt>国内实测</dt><dd><FieldTestBadge :result="details.field_test" detail /></dd></div><div><dt>出口 / 地区</dt><dd><NodeLocation :node="details" /></dd></div><div><dt>最近检测</dt><dd>{{ formatDate(details.last_checked_at) }}</dd></div></dl>
      <p v-if="details.last_error" class="notice error">{{ details.last_error }}</p>
      <NodeBadges :insights="details.insights" detail />
      <h3>关联来源 <span class="count">{{ details.sources.length }}</span></h3>
      <ul class="origin-list"><li v-for="source in details.sources" :key="source.id" class="origin-item">
        <div class="origin-head"><UserAvatar :id="source.owner_id" :name="source.provider_name || source.provider_username || '提供方'" :version="source.provider_avatar" :size="36" /><div class="origin-title"><strong :title="source.name">{{ source.name }}</strong><span class="small muted">{{ source.provider_name || source.provider_username || '提供方' }}<TitleChip :name="source.provider_title" /></span></div><button v-if="source.owner_id" class="compact" @click="viewProvider(source.owner_id)">提供方节点<AppIcon name="right" /></button></div>
        <div class="origin-facts"><span><small>原始名称</small><b :title="source.node_name">{{ source.node_name }}</b></span><span><small>等级门槛</small><b>{{ source.min_level ? `Lv${source.min_level}` : '不限' }}</b></span><span><small>有效席位</small><b :class="{ 'danger-text': source.unavailable === 'full' }">{{ source.active_users ?? 0 }} / {{ source.max_users || '不限' }}</b></span></div><p v-if="source.unavailable" class="small danger-text">{{ blockHint(source.unavailable, source.min_level) }}</p>
        <div class="origin-foot"><UsageHint :usage="source.subscription_usage" bar /><FeedbackBadge :summary="source.feedback" /></div>
      </li></ul>
      <FeedbackPanel v-if="!isGuest" :key="details.id" target="node" :node-id="details.id" :origins="details.sources.map(s => ({ id: s.id, name: s.name }))" :can-submit="!ownsAll(details)" @changed="load" />
      <div v-if="isAdmin" class="dialog-actions"><button :disabled="nodeTesting.has(details.id) || paused" @click="testNode(details)"><AppIcon name="probe" />{{ nodeTesting.has(details.id) ? '排队中…' : '检测节点' }}</button></div>
    </Modal>
    <Modal v-if="originNode" title="选择领取来源" @close="originNode = null"><p class="muted">「{{ originNode.name }}」由多个来源提供，请选择本次领取归属。</p><div class="origin-choices"><button v-for="source in origins(originNode)" :key="source.id" class="source-choice" :disabled="!originAllowed(source)" :title="originAllowed(source) ? source.name : blockHint(source.unavailable || 'level', source.min_level)" @click="chooseOrigin(source.id)"><AppIcon name="sources" /><span><strong :title="source.name">{{ source.name }}</strong><small>{{ source.provider_name || '提供方' }} · {{ source.min_level ? `最低 Lv${source.min_level}` : 'Lv0 不限' }}</small><small v-if="!originAllowed(source)" class="danger-text">{{ source.unavailable === 'full' ? '席位已满' : source.unavailable === 'kicked' ? '已被提供方移出' : `等级不足，当前 Lv${communityUser.level}` }}</small><UsageHint :usage="source.subscription_usage" /></span><AppIcon name="right" /></button></div><p class="small muted">同一账号从同一来源引用多个节点，只占一个有效席位；保存时如果你已占用其中某个来源的席位，会优先沿用它。所选来源下架后，订阅会自动切换到其他提供此节点的来源。</p></Modal>
    <Modal v-if="sourcesOpen" title="跟随整个来源" wide @close="sourcesOpen = false"><p class="small muted">跟随后订阅会实时同步该来源的全部可用节点，不占节点额度；设置了人数上限的来源占用来源额度。</p><div class="source-selection-layout"><SourceBrowser :provider-id="filters.provider_id" :following="!!filters.following" :selected="wholeSourceIDs" @choose="selectSource" /><DraftPreview /></div><p v-if="selectionError" class="notice warning" role="alert">{{ selectionError }}</p><div class="dialog-actions"><span class="small muted">已选 {{ draftNodes.length }} 个节点 · 跟随 {{ draftSources.length }} 个来源</span><button class="primary" @click="sourcesOpen = false">完成选择</button></div></Modal>
    <Modal v-if="reviewOpen" :title="editingSubscription ? '保存组合修改' : '保存组合订阅'" :busy="saving" @close="reviewOpen = false"><form @submit.prevent="save">
      <div class="form-field"><span class="field-label">保存方式</span><CustomSelect v-model="saveTarget" label="保存方式" :options="targetOptions" :disabled="saving || reviewLoading" /><span class="field-hint">覆盖会替换目标组合的节点和规则，保留原订阅链接。</span></div>
      <p v-if="subscriptionFull" class="notice info">订阅数量已满，请选择覆盖已有组合。</p>
      <p v-if="editingSubscription" class="notice warning">将用当前选区覆盖「{{ existingSubscriptions.find(item => item.id === editingSubscription)?.name || draftName }}」。其他组合仍合计占用账号额度。</p>
      <label class="form-field"><span class="field-label">订阅名称</span><input v-model="draftName" required maxlength="200" :disabled="saving" /></label>
      <RuleProfileSelect v-model="draftRuleProfile" :disabled="saving" />
      <div class="selection-summary"><strong>{{ draftNodes.length }} 个单选节点 · 跟随 {{ draftSources.length }} 个来源</strong><p class="small muted">本次涉及 {{ selectedSources.size }} 个来源（{{ limitedSources.size }} 个占来源额度），约 {{ draftEstimate }} 个节点（跨来源去重后可能更少），其中跟随来源的节点不占节点额度。</p><p class="small muted">账号已用 {{ profile?.usage.nodes ?? 0 }} / {{ limits.nodes }} 节点、{{ profile?.usage.sources ?? 0 }} / {{ limits.sources }} 来源。可选择覆盖已有组合以释放它的额度。</p></div>
      <p v-if="draftSources.length" class="notice info">跟随的来源上游增减节点后，订阅会自动同步；频繁更换跟随的来源会触发风控限制。</p><DraftPreview :disabled="saving" />
      <p v-if="reviewLoading" class="small muted" role="status">正在核对已有组合…</p><p v-if="saveError" class="notice error" role="alert">{{ saveError }}<button v-if="reviewFailed" type="button" @click="openReview">重试</button></p><div class="dialog-actions"><button type="button" :disabled="saving" @click="reviewOpen = false">继续选择</button><button class="primary" :disabled="saving || reviewLoading || reviewFailed || subscriptionFull || !draftName.trim() || (!draftNodes.length && !draftSources.length)">{{ saving ? '正在保存…' : editingSubscription ? '确认覆盖订阅' : '保存订阅' }}</button></div>
    </form></Modal>
  </section>
</template>

<style scoped>
.node-panel .node-filters {
  grid-template-columns: minmax(140px, 1.6fr) repeat(2, minmax(76px, 1fr)) minmax(76px, .9fr) minmax(110px, 1.2fr) repeat(2, minmax(76px, 1fr)) 32px;
  gap: 6px;
  padding: 10px;
}
.node-filters > .node-level-filter { width: 100%; min-width: 0; }
.node-filters .node-level-filter .custom-select { width: 100%; min-width: 0; }
.node-filters :deep(.custom-select-trigger), .node-filters :deep(.filter-trigger) { padding-inline: 7px; font-size: 12px; gap: 5px; }
@media (max-width: 1199px) and (min-width: 981px) {
  .node-panel .node-filters { grid-template-columns: minmax(110px, 1.5fr) repeat(3, minmax(58px, 1fr)) minmax(94px, 1.2fr) repeat(2, minmax(58px, 1fr)) 30px; gap: 4px; padding: 8px; }
  .node-filters :deep(.custom-select-trigger), .node-filters :deep(.filter-trigger) { padding-inline: 5px; font-size: 11px; }
}
@media (max-width: 980px) {
  .node-panel .node-filters { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; }
  .node-filters .search-field { grid-column: span 2; }
}
@media (max-width: 600px) {
  .node-panel .node-filters { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>
