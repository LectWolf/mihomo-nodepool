<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, isCancelled, useApi, type NodePage, type PoolNode, type Source } from '../api/system'
import { draftNodes, draftSources, isGuest, requireLogin, selectingNodes, selectionError, toggleNode, toggleSource } from '../api/accounts'
import { blockHint, blockLabel, selectionFor, timeAgo, useAccountScope } from '../community'
import { navigate } from '../router'
import { cached, remember } from '../view-cache'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import UserAvatar from './UserAvatar.vue'
import UsageHint from './UsageHint.vue'
import FeedbackBadge from './FeedbackBadge.vue'
// 经典版来源详情：左侧同页来源列表，右侧地区汇总与节点格子
const props = defineProps<{ list: Source[]; initial: number; paused: boolean }>()
const emit = defineEmits<{ close: [] }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const flags = import.meta.glob<string>('../../node_modules/flag-icons/flags/4x3/[a-z][a-z].svg', { eager: true, query: '?url&no-inline', import: 'default' })
const names = typeof Intl.DisplayNames === 'function' ? new Intl.DisplayNames(['zh-CN'], { type: 'region' }) : null
const currentID = ref(props.initial)
const source = computed(() => props.list.find(s => s.id === currentID.value) ?? props.list[0])
const nodes = ref<PoolNode[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref('')
const region = ref('')
let controller: AbortController | undefined
let sequence = 0
const path = (id: number) => `/nodes?${new URLSearchParams({ source_id: String(id), page: '1', page_size: '200', sort: 'latency' })}`
async function load() {
  const s = source.value
  if (!s || props.paused) return
  const ticket = scope.ticket(), current = ++sequence, key = path(s.id)
  controller?.abort(); controller = new AbortController()
  const saved = cached<NodePage>(key)
  if (saved) { nodes.value = saved.items; total.value = saved.total } else { nodes.value = []; total.value = 0 }
  loading.value = true; error.value = ''
  try {
    const result = await api<NodePage>(key, { signal: controller.signal })
    if (current === sequence && scope.current(ticket)) { nodes.value = result.items; total.value = result.total; remember(key, result) }
  } catch (cause) { if (!isCancelled(cause) && current === sequence && scope.current(ticket)) error.value = errorText(cause) }
  finally { if (current === sequence) loading.value = false }
}
watch(currentID, () => { region.value = ''; void load() }, { immediate: true })
onBeforeUnmount(() => { ++sequence; controller?.abort() })
function flag(code: string) { return code ? flags[`../../node_modules/flag-icons/flags/4x3/${code.toLowerCase()}.svg`] : undefined }
function country(code: string) { try { return (code && names?.of(code)) || code || '未知' } catch { return code || '未知' } }
function tone(s: Source) { const h = s.node_count ? (s.healthy_count ?? 0) / s.node_count : 0; return !s.healthy_count ? 'down' : h >= 0.6 ? 'up' : 'partial' }
const regions = computed(() => {
  const map = new Map<string, number>()
  for (const n of nodes.value) map.set(n.country_code, (map.get(n.country_code) ?? 0) + 1)
  return [...map.entries()].sort((a, b) => b[1] - a[1]).map(([code, count]) => ({ code, count }))
})
const shown = computed(() => region.value ? nodes.value.filter(n => n.country_code === region.value) : nodes.value)
const picked = computed(() => !!source.value && draftSources.value.some(v => v.id === source.value.id))
const chosen = computed(() => new Set(draftNodes.value.filter(n => n.source_id === source.value?.id).map(n => n.node_id)))
const seats = computed(() => { const s = source.value; return s?.max_users ? `${s.occupied_users ?? s.active_users ?? 0} / ${s.max_users} 席` : '不限人数' })
function latencyTone(n: PoolNode) { if (n.status !== 'healthy' || n.latency_ms == null) return 'off'; return n.latency_ms < 120 ? 'fast' : n.latency_ms < 250 ? 'mid' : 'slow' }
function follow() {
  const s = source.value
  if (!s || requireLogin('登录后即可跟随整个来源。')) return
  if (s.unavailable && !picked.value) { selectionError.value = blockHint(s.unavailable, s.min_level); return }
  toggleSource(s)
  if (picked.value) selectingNodes.value = true
}
// 单选节点：归属固定为当前来源
function pick(n: PoolNode) {
  if (isGuest.value) { requireLogin('登录后即可挑选节点、组合自己的订阅。'); return }
  const s = source.value, origin = n.sources.find(v => v.id === s?.id)
  if (!s || !origin || picked.value) return
  const existing = draftNodes.value.find(v => v.node_id === n.id && v.source_id === s.id)
  if (existing) { toggleNode(existing); return }
  if (s.unavailable) { selectionError.value = blockHint(s.unavailable, s.min_level); return }
  toggleNode(selectionFor(n, origin)); selectingNodes.value = true
}
function viewInNodes() { const s = source.value; emit('close'); if (s) navigate('nodes', new URLSearchParams({ source_id: String(s.id) })) }
function step(offset: number) {
  const index = props.list.findIndex(s => s.id === currentID.value)
  const next = props.list[index + offset]
  if (next) currentID.value = next.id
}
</script>

<template>
  <Modal class="classic-source-dialog" title="来源详情" expanded @close="emit('close')">
    <div class="csd-layout">
      <nav class="csd-list" aria-label="本页来源" @keydown.down.prevent="step(1)" @keydown.up.prevent="step(-1)">
        <p class="csd-list-title">本页来源</p>
        <button v-for="s in list" :key="s.id" type="button" :class="{ on: s.id === currentID, unavailable: !!s.unavailable }" :aria-current="s.id === currentID ? 'true' : undefined" @click="currentID = s.id"><span class="csd-list-name" :title="s.name">{{ s.name }}</span><small>{{ s.healthy_count ?? 0 }}/{{ s.node_count }}</small><span :class="['status-ball', tone(s)]"></span></button>
      </nav>
      <section v-if="source" class="csd-main" :aria-busy="loading">
        <header class="csd-head">
          <div class="csd-title"><h3 :title="source.name">{{ source.name }}</h3><span :class="['status-ball', tone(source)]"></span></div>
          <div class="csd-actions"><button type="button" class="compact" @click="viewInNodes">节点页查看<AppIcon name="right" /></button><button v-if="!isGuest" type="button" :class="['compact', { primary: !picked && !source.unavailable }]" :disabled="!!source.unavailable && !picked" :title="source.unavailable ? blockHint(source.unavailable, source.min_level) : '跟随后自动同步该来源全部可用节点'" @click="follow"><AppIcon :name="picked ? 'check' : 'plus'" />{{ picked ? '已跟随 · 点击取消' : '跟随来源' }}</button><button v-else type="button" class="primary compact" @click="requireLogin('登录后即可跟随整个来源。')">登录后使用</button></div>
        </header>
        <div class="csd-sub"><span class="classic-provider"><UserAvatar :id="source.owner_id" :name="source.provider_name || source.provider_username || '提供方'" :version="source.provider_avatar" :size="18" />{{ source.provider_name || source.provider_username || '提供方' }}</span><span v-if="source.last_refresh_at" class="classic-muted">{{ timeAgo(source.last_refresh_at) }}更新</span></div>
        <div class="classic-pills"><span v-if="source.private" class="classic-pill private"><AppIcon name="lock" />{{ source.shared_with_me ? '提供方分享给我' : '私有来源' }}</span><span v-else class="classic-pill">{{ source.min_level ? `Lv${source.min_level}+` : '不限等级' }}</span><span class="classic-pill" :class="{ bad: source.unavailable === 'full' }">{{ seats }}</span><span class="classic-pill">{{ source.healthy_count ?? 0 }}/{{ source.node_count }} 健康</span><span v-if="source.avg_latency" class="classic-pill">平均 {{ source.avg_latency }} ms</span><span v-if="source.unavailable" class="classic-pill bad">{{ blockLabel(source.unavailable, source.min_level) }}</span><span v-else-if="source.seated" class="classic-pill good">使用中</span><span v-if="!source.max_users || source.private" class="classic-pill new" title="未设置人数上限，选用它不占你的来源额度">不占来源额度</span><FeedbackBadge :summary="source.feedback" /></div>
        <UsageHint :usage="source.subscription_usage" bar />
        <p v-if="source.unavailable" class="small danger-text">{{ blockHint(source.unavailable, source.min_level) }}</p>
        <p v-if="error" class="notice error" role="alert">{{ error }}<button type="button" @click="load">重试</button></p>
        <div v-if="regions.length > 1" class="csd-regions" role="group" aria-label="按地区筛选节点"><button v-for="r in regions" :key="r.code" type="button" :class="{ on: region === r.code }" :aria-pressed="region === r.code" @click="region = region === r.code ? '' : r.code"><img v-if="flag(r.code)" :src="flag(r.code)" alt="" width="18" height="13" /><span>{{ country(r.code) }}</span><small>{{ r.count }}</small></button></div>
        <p v-if="!isGuest && !picked && nodes.length" class="small muted csd-hint">点击节点可单独加入订阅；跟随整个来源后会自动同步全部节点。</p>
        <div v-if="shown.length" class="csd-nodes">
          <button v-for="n in shown" :key="n.id" type="button" :class="['csd-node', latencyTone(n), { on: picked || chosen.has(n.id) }]" :title="n.status === 'unhealthy' ? (n.last_error || '检测不可用') : n.name" :aria-pressed="picked || chosen.has(n.id)" :disabled="picked" @click="pick(n)">
            <img v-if="flag(n.country_code)" :src="flag(n.country_code)" alt="" width="24" height="18" /><span v-else class="csd-flag-empty"></span>
            <span class="csd-node-text"><strong>{{ n.name }}</strong><small>{{ n.status === 'healthy' && n.latency_ms != null ? `${n.latency_ms}ms` : n.status === 'unhealthy' ? '不可用' : '待检测' }} · {{ n.type.toUpperCase() }}</small></span>
            <AppIcon v-if="picked || chosen.has(n.id)" name="check" />
          </button>
        </div>
        <div v-else-if="loading" class="empty-state" role="status"><span class="spinner"></span><h3>正在读取节点</h3></div>
        <div v-else-if="!error" class="empty-state"><h3>暂无可展示的节点</h3></div>
        <p v-if="total > nodes.length" class="small muted">仅显示前 {{ nodes.length }} 个节点，共 {{ total }} 个，可到节点页查看全部。</p>
      </section>
    </div>
  </Modal>
</template>
