<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, useApi, type SystemStatistics, type TaskItem } from '../api/system'
import { formatBytes, useAccountScope, type SubscriptionUsage } from '../community'
import { usePolling } from '../polling'
import { isAdmin } from '../api/accounts'
import AppIcon from './AppIcon.vue'
import PageNumbers from './PageNumbers.vue'
import BarChart from './BarChart.vue'
import SiteTrafficPanel from './SiteTrafficPanel.vue'
import '../statistics.css'
import { cached, remember } from '../view-cache'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const data = ref<SystemStatistics | null>(cached<SystemStatistics>('/statistics') ?? null)
const loading = ref(false)
const error = ref('')
const usagePage = ref(1)
const usagePages = computed(() => Math.max(1, Math.ceil((data.value?.usage.items.length ?? 0) / 20)))
const usageItems = computed(() => data.value?.usage.items.slice((usagePage.value - 1) * 20, usagePage.value * 20) ?? [])
watch(usagePages, max => { usagePage.value = Math.min(usagePage.value, max) })
let sequence = 0
let controller: AbortController | undefined
async function load() {
  if (props.paused || loading.value) return
  const current = ++sequence, ticket = scope.ticket()
  controller = new AbortController(); loading.value = true
  try {
    const result = await api<SystemStatistics>('/statistics', { signal: controller.signal })
    if (current === sequence && scope.current(ticket)) { data.value = result; error.value = ''; remember('/statistics', result) }
  } catch (cause) {
    if (current === sequence && scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause)
  } finally { if (current === sequence) loading.value = false }
}
watch(() => props.paused, paused => {
  if (paused) { ++sequence; controller?.abort(); loading.value = false }
  else void load()
}, { immediate: true })
// 普通用户与访客读取的是服务端 5 秒缓存，降低轮询频率
usePolling(async () => { if (!props.paused) await load() }, 15000, false)
onBeforeUnmount(() => { ++sequence; controller?.abort() })
const usageLabels: Record<string, string> = { unknown: '未知', normal: '正常', low: '余量偏低', exhausted: '流量耗尽', expired: '已到期', invalid: '信息异常' }
function bytes(value: number | null | undefined) { return value == null ? '未知' : formatBytes(value) }
function taskTime(task: TaskItem) { return task.status === 'processing' && task.lease_until ? `租约至 ${formatDate(task.lease_until)}` : task.next_at ? `计划 ${formatDate(task.next_at)}` : '等待调度' }
// 主值显示最大的两级单位，余下部分放到副行
function uptime(seconds: number) {
  const d = Math.floor(seconds / 86400), h = Math.floor(seconds % 86400 / 3600), m = Math.floor(seconds % 3600 / 60)
  return d ? `${d} 天 ${h} 小时` : h ? `${h} 小时 ${m} 分` : `${m} 分 ${seconds % 60} 秒`
}
function uptimeRest(seconds: number) {
  const m = Math.floor(seconds % 3600 / 60), sec = seconds % 60
  return seconds >= 86400 ? `${m} 分 ${sec} 秒` : seconds >= 3600 ? `${sec} 秒` : '刚启动不久'
}
const nodeHealth = computed(() => {
  const o = data.value?.overview
  return o ? [{ key: 'healthy', value: o.nodes_healthy }, { key: 'unhealthy', value: o.nodes_unhealthy }, { key: 'unknown', value: o.nodes_unknown }].filter(part => part.value > 0) : []
})
function usageTone(status?: string) {
  if (status === 'normal') return 'ready'
  if (status === 'low') return 'queued'
  if (status === 'expired' || status === 'exhausted' || status === 'invalid') return 'error'
  return 'unknown'
}
function remainingPercent(usage?: SubscriptionUsage | null) {
  if (!usage || usage.total_bytes == null || usage.remaining_bytes == null || usage.total_bytes <= 0) return null
  return Math.min(100, Math.max(0, usage.remaining_bytes / usage.total_bytes * 100))
}
function successRate(runs: number, errors: number) { return runs > 0 ? Math.min(100, Math.max(0, (runs - errors) / runs * 100)) : null }
function formatRate(runs: number, errors: number) { const rate = successRate(runs, errors); return rate == null ? '' : `${rate >= 99.95 ? '100' : rate.toFixed(1)}%` }
const pad = (n: number) => String(n).padStart(2, '0')
const hourlyBars = computed(() => (data.value?.traffic?.hourly ?? []).map(p => { const d = new Date(p.at); return { label: `${pad(d.getHours())}时`, value: p.bytes, detail: `${d.getMonth() + 1}月${d.getDate()}日 ${pad(d.getHours())}:00–${pad((d.getHours() + 1) % 24)}:00` } }))
const dailyBars = computed(() => (data.value?.traffic?.daily ?? []).map(p => { const d = new Date(p.at); return { label: `${d.getMonth() + 1}/${d.getDate()}`, value: p.bytes, detail: `${d.getMonth() + 1}月${d.getDate()}日` } }))
const formatTraffic = (v: number) => formatBytes(v)
function rateTone(runs: number, errors: number) { const rate = successRate(runs, errors); return rate == null ? 'unknown' : rate >= 90 ? 'ready' : rate >= 70 ? 'queued' : 'error' }
</script>
<template>
  <section class="statistics-page">
    <div class="page-heading"><div><p class="eyebrow">OPERATIONS & USAGE</p><h1>运行统计</h1><p class="muted">服务规模、后台任务与来源套餐用量，每 15 秒自动采样。</p></div><div class="statistics-actions"><span v-if="data" class="statistics-sample" :class="{ stale: !!error }"><i class="live-dot" aria-hidden="true"></i>最近采样 {{ formatDate(data.sampled_at) }}</span><button :disabled="loading || paused" @click="load"><span v-if="loading" class="spinner"></span><AppIcon v-else name="refresh" />{{ loading ? '读取中…' : '刷新统计' }}</button></div></div>
    <p v-if="error" class="notice error" role="alert">{{ error }}<span v-if="data">保留上次采样结果。</span><button :disabled="loading || paused" @click="load">重试</button></p>
    <div v-if="!data && loading" class="panel empty-state" role="status"><span class="spinner"></span><h3>正在读取统计</h3></div>
    <template v-if="data">
      <div class="stats-grid statistics-overview">
        <article class="stat-card metric tone-green"><span><i class="metric-icon"><AppIcon name="sources" /></i>启用来源</span><strong>{{ data.overview.sources_enabled.toLocaleString() }}</strong><small>共 {{ data.overview.sources_total.toLocaleString() }} 个 · {{ data.overview.providers_total }} 个提供方</small></article>
        <article class="stat-card metric tone-blue"><span><i class="metric-icon"><AppIcon name="nodes" /></i>有效节点</span><strong>{{ data.overview.nodes_visible.toLocaleString() }}</strong>
          <div class="segment-bar" role="img" :aria-label="`健康 ${data.overview.nodes_healthy}，不可用 ${data.overview.nodes_unhealthy}，未检测 ${data.overview.nodes_unknown}`"><i v-for="part in nodeHealth" :key="part.key" :class="part.key" :style="{ flexGrow: part.value }"></i></div>
          <small class="legend"><span class="healthy">{{ data.overview.nodes_healthy.toLocaleString() }} 健康</span><span class="unhealthy">{{ data.overview.nodes_unhealthy.toLocaleString() }} 不可用</span><span class="unknown">{{ data.overview.nodes_unknown.toLocaleString() }} 未检测</span></small></article>
        <article class="stat-card metric tone-violet"><span><i class="metric-icon"><AppIcon name="users" /></i>启用账号</span><strong>{{ data.overview.users_enabled.toLocaleString() }}</strong><small>共 {{ data.overview.users_total.toLocaleString() }} 个账号</small></article>
        <article class="stat-card metric tone-amber"><span><i class="metric-icon"><AppIcon name="link" /></i>组合订阅</span><strong>{{ data.overview.subscriptions.toLocaleString() }}</strong><small>{{ data.overview.active_seats.toLocaleString() }} 个有效席位</small></article>
      </div>
      <div class="stats-grid statistics-secondary">
        <article class="stat-card pulse" :class="{ running: data.tasks.active_sources > 0 }"><span><i class="live-dot" aria-hidden="true"></i>来源任务</span><strong>{{ data.tasks.active_sources }}<em>执行中</em></strong><small><b class="pill">{{ data.tasks.pending_sources }} 个排队</b></small></article>
        <article class="stat-card pulse" :class="{ running: data.tasks.active_nodes > 0 }"><span><i class="live-dot" aria-hidden="true"></i>节点任务</span><strong>{{ data.tasks.active_nodes }}<em>执行中</em></strong><small><b class="pill">{{ data.tasks.pending_nodes.toLocaleString() }} 个排队</b></small></article>
        <article class="stat-card pulse"><span><AppIcon name="shield" />屏蔽节点</span><strong>{{ data.overview.nodes_blocked.toLocaleString() }}</strong><small>数据库保存 {{ data.overview.nodes_total.toLocaleString() }} 个去重节点</small></article>
        <article class="stat-card pulse"><span><AppIcon name="check" />来源状态</span><strong>{{ data.overview.sources_ready }}<em>就绪</em></strong><small><b class="pill" :class="{ danger: data.overview.sources_error > 0 }">{{ data.overview.sources_error }} 个失败</b></small></article>
      </div>
      <section class="panel traffic-panel">
        <div class="panel-heading"><div><h2>流量消耗</h2><p>每个整点读取来源套餐的已用流量，按相邻快照的差值汇总；套餐重置时取重置后的用量。</p></div></div>
        <div class="traffic-charts">
          <div><h3><AppIcon name="clock" />近 24 小时</h3><BarChart :bars="hourlyBars" :format="formatTraffic" tone="blue" bytes :every="4" empty="需要连续两个整点快照才能算出用量，通常一小时内出现" /></div>
          <div><h3><AppIcon name="chart" />近 30 天</h3><BarChart :bars="dailyBars" :format="formatTraffic" tone="violet" bytes :every="5" empty="暂无按天统计" /></div>
        </div>
        <p class="small muted">只统计返回 Subscription-Userinfo 的来源；同一套餐被重复导入时会重复计数，可在运行设置中关闭整点用量轮询。</p>
      </section>
      <SiteTrafficPanel v-if="isAdmin && data.site_traffic" :data="data.site_traffic" />
      <section class="panel">
        <div class="panel-heading"><div><h2>当前后台任务</h2><p>执行状态以任务租约为准，排队包括已到期任务；每类最多显示 100 条，优先显示执行中任务。</p></div></div>
        <div class="task-columns">
          <div v-for="category in ([{ key: 'source', title: '来源任务', icon: 'sources', items: data.source_tasks }, { key: 'node', title: '节点任务', icon: 'nodes', items: data.node_tasks }])" :key="category.key" class="task-column">
            <h3><AppIcon :name="category.icon" />{{ category.title }} <span class="count">展示 {{ category.items.length }} 条</span></h3>
            <ul class="statistics-task-list"><li v-for="task in category.items" :key="task.id" :class="task.status"><i class="live-dot" aria-hidden="true"></i><span><strong :title="task.name">{{ task.name }}</strong><small><code>#{{ task.id }}</code> · {{ task.type }} · {{ taskTime(task) }}</small><small v-if="task.last_error" class="danger-text">最近错误：{{ task.last_error }}</small></span><span class="badge" :class="task.status === 'processing' ? 'running' : 'queued'">{{ task.status === 'processing' ? '执行中' : '排队中' }}</span></li></ul>
            <p v-if="!category.items.length" class="task-empty small muted">当前没有{{ category.title }}。</p>
          </div>
        </div>
      </section>
      <section class="panel">
        <div class="panel-heading"><div><h2>来源套餐流量</h2><p>上游套餐用量快照，随来源刷新更新。{{ data.usage.sources_known }} / {{ data.usage.sources_total }} 个来源提供了套餐信息。</p></div></div>
        <p class="small muted">每个来源计一次，包含停用来源；同一上游套餐被多个来源重复导入时仍会重复计数。以下为已知字段的小计，未知值不参与，不能视为平台实际总流量或个人用量。</p>
        <p v-if="data.usage.overflow" class="notice warning">部分汇总超过整数上限，该项暂不显示数值。</p>
        <div class="usage-stat-grid">
          <div class="tone-blue"><span><AppIcon name="upload" />已上传小计</span><strong>{{ bytes(data.usage.upload_bytes) }}</strong><small>{{ data.usage.upload_sources }} 个来源</small></div>
          <div class="tone-violet"><span><AppIcon name="down" />已下载小计</span><strong>{{ bytes(data.usage.download_bytes) }}</strong><small>{{ data.usage.download_sources }} 个来源</small></div>
          <div class="tone-amber"><span><AppIcon name="chart" />套餐总量小计</span><strong>{{ bytes(data.usage.total_bytes) }}</strong><small>{{ data.usage.total_sources }} 个来源</small></div>
          <div class="tone-green"><span><AppIcon name="heart" />剩余流量小计</span><strong>{{ bytes(data.usage.remaining_bytes) }}</strong><small>{{ data.usage.remaining_sources }} 个来源</small></div>
        </div>
        <div class="table-wrap statistics-usage-table"><table><thead><tr><th>来源</th><th>已上传 / 已下载</th><th>剩余 / 总量</th><th>套餐状态</th><th>到期 / 数据更新</th></tr></thead><tbody><tr v-for="item in usageItems" :key="item.id" :class="{ disabled: !item.enabled }"><td><strong :title="item.name">{{ item.name }}</strong><small class="cell-sub">#{{ item.id }} · {{ item.enabled ? '启用' : '停用' }}</small></td><td><span class="traffic up">{{ bytes(item.usage?.upload_bytes) }}</span><small class="cell-sub traffic down">{{ bytes(item.usage?.download_bytes) }}</small></td><td><div class="quota"><div class="quota-text"><span>{{ bytes(item.usage?.remaining_bytes) }}</span><small>/ {{ bytes(item.usage?.total_bytes) }}</small></div><div class="meter" :class="usageTone(item.usage?.status)"><i v-if="remainingPercent(item.usage) != null" :style="{ width: remainingPercent(item.usage) + '%' }"></i></div></div></td><td><span class="badge" :class="usageTone(item.usage?.status)">{{ usageLabels[item.usage?.status || 'unknown'] || usageLabels.unknown }}</span></td><td>{{ item.usage?.expires_at ? formatDate(item.usage.expires_at) : '到期未知' }}<small class="cell-sub">{{ item.usage?.fetched_at ? formatDate(item.usage.fetched_at) : '更新未知' }}</small></td></tr></tbody></table></div>
        <p v-if="!data.usage.items.length" class="small muted">暂无来源。</p>
        <div class="compact-pagination"><span class="small muted">共 {{ data.usage.sources_total }} 个来源</span><PageNumbers v-model="usagePage" :pages="usagePages" label="来源流量分页" previous-label="上一页来源流量" next-label="下一页来源流量" /></div>
      </section>
      <section class="panel">
        <div class="panel-heading"><div><h2>本次服务运行</h2><p>启动于 {{ formatDate(data.runtime.started_at) }}；以下计数重启后重新累计。</p></div></div>
        <div class="usage-stat-grid runtime-stat-grid">
          <div><span><AppIcon name="clock" />运行时长</span><strong>{{ uptime(data.runtime.uptime_seconds) }}</strong><small>{{ uptimeRest(data.runtime.uptime_seconds) }}</small></div>
          <div v-if="isAdmin"><span><AppIcon name="settings" />Go 堆内存 / 协程</span><strong>{{ bytes(data.runtime.heap_bytes) }}</strong><small>{{ data.runtime.goroutines }} 个协程</small></div>
          <div v-else><span><AppIcon name="history" />服务启动于</span><strong>{{ formatDate(data.runtime.started_at).split(' ')[0] }}</strong><small>{{ formatDate(data.runtime.started_at).split(' ')[1] }}</small></div>
          <div><span><AppIcon name="sources" />订阅正文下载量</span><strong>{{ bytes(data.runtime.source_body_bytes) }}</strong><div class="meter rate" :class="rateTone(data.runtime.source_runs, data.runtime.source_errors)"><i :style="{ width: (successRate(data.runtime.source_runs, data.runtime.source_errors) ?? 0) + '%' }"></i></div><small>来源处理 {{ data.runtime.source_runs.toLocaleString() }} 次 · 失败 {{ data.runtime.source_errors.toLocaleString() }} 次<b v-if="successRate(data.runtime.source_runs, data.runtime.source_errors) != null">{{ formatRate(data.runtime.source_runs, data.runtime.source_errors) }}</b></small></div>
          <div><span><AppIcon name="probe" />节点检测任务完成</span><strong>{{ data.runtime.probe_runs.toLocaleString() }} 次</strong><div class="meter rate" :class="rateTone(data.runtime.probe_runs, data.runtime.probe_errors)"><i :style="{ width: (successRate(data.runtime.probe_runs, data.runtime.probe_errors) ?? 0) + '%' }"></i></div><small>成功 {{ (data.runtime.probe_runs - data.runtime.probe_errors).toLocaleString() }} 次 · 失败 {{ data.runtime.probe_errors.toLocaleString() }} 次<b v-if="successRate(data.runtime.probe_runs, data.runtime.probe_errors) != null">{{ formatRate(data.runtime.probe_runs, data.runtime.probe_errors) }}</b></small></div>
        </div>
        <p class="small muted">正文下载量包含失败响应中实际读取的正文，不含协议开销、检测流量或出口查询流量。客户端直接连接节点，个人代理流量不经过此服务，无法由此统计。</p>
      </section>
    </template>
  </section>
</template>
