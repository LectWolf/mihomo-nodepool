<script setup lang="ts">
import { computed, ref } from 'vue'
import type { SiteTraffic, SiteTrafficRow } from '../api/system'
import { formatBytes } from '../community'
import AppIcon from './AppIcon.vue'

const props = defineProps<{ data: SiteTraffic }>()

// 分组：图表按组堆叠，摘要卡片按组汇总；订阅拉取单独一组，避免被网页接口的体积稀释
type Group = 'probe' | 'source' | 'serve' | 'sub'
const groups: { key: Group; label: string; tone: string }[] = [
  { key: 'probe', label: '节点检测', tone: 'amber' },
  { key: 'source', label: '来源与用量', tone: 'violet' },
  { key: 'serve', label: '对外服务', tone: 'blue' },
  { key: 'sub', label: '订阅拉取', tone: 'teal' },
]
const kindGroups: Record<string, Group> = {
  probe: 'probe', exit: 'probe', tls: 'probe',
  source: 'source', usage: 'source',
  api: 'serve', web: 'serve', link: 'serve', beacon: 'serve', lease: 'serve',
  sub: 'sub',
}
const total = (r: { bytes_in: number; bytes_out: number }) => r.bytes_in + r.bytes_out
const groupOf = (kind: string) => kindGroups[kind]

const pad = (n: number) => String(n).padStart(2, '0')
interface Stack { label: string; detail: string; parts: Record<Group, number>; sum: number; nic: number }
function stacks(rows: SiteTrafficRow[], slots: Date[], key: (d: Date) => string, label: (d: Date) => string, detail: (d: Date) => string): Stack[] {
  const map = new Map<string, Stack>()
  for (const d of slots) map.set(key(d), { label: label(d), detail: detail(d), parts: { probe: 0, source: 0, serve: 0, sub: 0 }, sum: 0, nic: 0 })
  for (const r of rows) {
    const s = map.get(key(new Date(r.at)))
    if (!s) continue
    if (r.kind === 'nic') { s.nic += total(r); continue }
    const g = groupOf(r.kind)
    if (!g) continue
    s.parts[g] += total(r); s.sum += total(r)
  }
  return [...map.values()]
}
const hourly = computed(() => {
  const now = new Date(); now.setMinutes(0, 0, 0)
  const slots = Array.from({ length: 24 }, (_, i) => new Date(now.getTime() - (23 - i) * 3600e3))
  return stacks(props.data.hourly, slots, d => String(d.getTime()), d => `${pad(d.getHours())}时`, d => `${d.getMonth() + 1}月${d.getDate()}日 ${pad(d.getHours())}:00–${pad((d.getHours() + 1) % 24)}:00`)
})
// 日界与服务端一致，按北京时间零点
const daily = computed(() => {
  const today = Math.floor((Date.now() + 8 * 3600e3) / 86400e3) * 86400e3 - 8 * 3600e3
  const slots = Array.from({ length: 30 }, (_, i) => new Date(today - (29 - i) * 86400e3))
  const bj = (d: Date) => new Date(d.getTime() + 8 * 3600e3)
  return stacks(props.data.daily, slots, d => String(d.getTime()), d => `${bj(d).getUTCMonth() + 1}/${bj(d).getUTCDate()}`, d => `${bj(d).getUTCMonth() + 1}月${bj(d).getUTCDate()}日`)
})
function ceiling(max: number) {
  if (max <= 0) return 1
  const base = 1024 ** Math.floor(Math.log(max) / Math.log(1024))
  const v = max / base, p = 10 ** Math.floor(Math.log10(v)), n = v / p
  return (n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10) * p * base
}
const charts = computed(() => [
  { key: 'hourly', title: '近 24 小时', icon: 'clock', bars: hourly.value, every: 4 },
  { key: 'daily', title: '近 30 天', icon: 'chart', bars: daily.value, every: 5 },
].map(c => ({ ...c, top: ceiling(Math.max(0, ...c.bars.map(b => b.sum))), sum: c.bars.reduce((n, b) => n + b.sum, 0), nic: c.bars.reduce((n, b) => n + b.nic, 0) })))
const active = ref<{ chart: string; index: number } | null>(null)
function shown(chart: string) {
  const c = charts.value.find(x => x.key === chart)
  return active.value?.chart === chart && c ? c.bars[active.value.index] : null
}

// 摘要卡片与占比按近 24 小时的分组汇总
const groupBytes = computed(() => {
  const bytes: Record<string, number> = {}
  for (const r of props.data.hourly) {
    const g = groupOf(r.kind)
    if (g) bytes[g] = (bytes[g] ?? 0) + total(r)
  }
  return bytes
})
const counted = computed(() => Object.values(groupBytes.value).reduce((n, v) => n + v, 0))
const groupTotals = computed(() => groups.map(g => ({ ...g, bytes: groupBytes.value[g.key] ?? 0 })))
const nic24 = computed(() => props.data.hourly.filter(r => r.kind === 'nic').reduce((acc, r) => ({ bytes_in: acc.bytes_in + r.bytes_in, bytes_out: acc.bytes_out + r.bytes_out }), { bytes_in: 0, bytes_out: 0 }))
const share = (v: number) => counted.value > 0 ? `${(v / counted.value * 100).toFixed(v / counted.value >= 0.1 ? 0 : 1)}%` : '—'
</script>

<template>
  <section class="panel site-traffic">
    <div class="panel-heading"><div><h2>本站流量</h2><p>服务自身收发的字节，按功能分类，每分钟写入一次。接收、发送都以本服务为准：对外服务的接收是请求、发送是响应；后台任务的接收是下载、发送是上传。</p></div></div>
    <div class="usage-stat-grid site-traffic-summary">
      <div v-for="g in groupTotals" :key="g.key" :class="`tone-${g.tone}`"><span><i class="swatch" aria-hidden="true"></i>{{ g.label }}</span><strong>{{ formatBytes(g.bytes) }}</strong><small>近 24 小时 · {{ share(g.bytes) }}</small></div>
      <div class="tone-green"><span><AppIcon name="globe" />容器网卡</span><strong>{{ formatBytes(nic24.bytes_in + nic24.bytes_out) }}</strong><small>接收 {{ formatBytes(nic24.bytes_in) }} · 发送 {{ formatBytes(nic24.bytes_out) }}</small></div>
    </div>
    <div class="traffic-charts">
      <div v-for="c in charts" :key="c.key">
        <h3><AppIcon :name="c.icon" />{{ c.title }}</h3>
        <div class="bar-chart">
          <div class="bar-chart-head"><span class="small muted">{{ shown(c.key)?.detail ?? '合计' }}</span><strong>{{ formatBytes(shown(c.key)?.sum ?? c.sum) }}</strong></div>
          <div class="stack-legend small"><span v-for="g in groups" :key="g.key" :class="`tone-${g.tone}`"><i class="swatch"></i>{{ g.label }} {{ formatBytes(shown(c.key)?.parts[g.key] ?? c.bars.reduce((n, b) => n + b.parts[g.key], 0)) }}</span><span class="muted">网卡 {{ formatBytes(shown(c.key)?.nic ?? c.nic) }}</span></div>
          <div class="bar-chart-body" style="height: 160px" @mouseleave="active = null">
            <div class="bar-grid" aria-hidden="true"><span>{{ formatBytes(c.top) }}</span><span>{{ formatBytes(c.top / 2) }}</span><span>0</span></div>
            <div v-if="!c.sum" class="bar-empty small muted">暂无数据，启动后一分钟内出现</div>
            <div class="bar-track" role="list">
              <button v-for="(bar, index) in c.bars" :key="index" type="button" role="listitem" class="bar stack-bar" :class="{ active: active?.chart === c.key && active.index === index }" :aria-label="`${bar.detail}：${formatBytes(bar.sum)}`" @mouseenter="active = { chart: c.key, index }" @focus="active = { chart: c.key, index }" @blur="active = null">
                <span class="stack" :style="{ height: `${bar.sum > 0 ? Math.max(2, bar.sum / c.top * 100) : 0}%` }"><i v-for="g in groups" :key="g.key" :class="`tone-${g.tone}`" :style="{ flexGrow: bar.parts[g.key] }"></i></span>
              </button>
            </div>
          </div>
          <div class="bar-axis" aria-hidden="true"><span v-for="(bar, index) in c.bars" :key="index">{{ index % c.every === 0 || index === c.bars.length - 1 ? bar.label : '' }}</span></div>
        </div>
      </div>
    </div>
    <p class="small muted">字节在应用层统计，不含 TCP/IP 包头、重传和 DNS 查询，握手多的检测流量在网卡上会更高一些。订阅、网页按 Nginx 压缩前的大小计算，经压缩后出站更小。容器网卡包含 Nginx 与本服务之间、本服务与数据库之间的流量，可用来对照上面各项是否漏算。</p>
  </section>
</template>
