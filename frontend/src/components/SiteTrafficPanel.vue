<script setup lang="ts">
import { computed, ref } from 'vue'
import type { SiteTraffic, SiteTrafficRow } from '../api/system'
import { formatBytes } from '../community'
import AppIcon from './AppIcon.vue'

const props = defineProps<{ data: SiteTraffic }>()

// 分组：图表按组堆叠，表格按分类与明细展开
type Group = 'probe' | 'source' | 'serve'
const groups: { key: Group; label: string; tone: string }[] = [
  { key: 'probe', label: '节点检测', tone: 'amber' },
  { key: 'source', label: '来源与用量', tone: 'violet' },
  { key: 'serve', label: '对外服务', tone: 'blue' },
]
const kinds: Record<string, { label: string; group?: Group; hint: string }> = {
  probe: { label: '延迟检测', group: 'probe', hint: '经节点访问测试地址，按协议区分' },
  exit: { label: '出口国家查询', group: 'probe', hint: '检测成功后经节点查询出口 IP' },
  tls: { label: '证书指纹读取', group: 'probe', hint: '直连跳过证书验证的 TLS 节点' },
  source: { label: '来源刷新', group: 'source', hint: '下载上游订阅' },
  usage: { label: '套餐用量读取', group: 'source', hint: '整点读取 Subscription-Userinfo；GET 回退时断开前已收下的正文不在计数里' },
  sub: { label: '订阅拉取', group: 'serve', hint: '用户客户端拉取组合订阅' },
  link: { label: '来源直链', group: 'serve', hint: '提供方的来源直链' },
  beacon: { label: '实测回报', group: 'serve', hint: '客户端测速经节点回报' },
  lease: { label: '席位保活', group: 'serve', hint: 'WebSocket 保活连接' },
  api: { label: '网页接口', group: 'serve', hint: '控制台的 /api 请求' },
  web: { label: '页面与静态资源', group: 'serve', hint: '前端页面、脚本和图片' },
  mysql: { label: '数据库', hint: '与 MySQL 的往返；数据库在本机或同一 Docker 网络时不走公网' },
  health: { label: '健康检查', hint: '容器内回环地址，不走网卡' },
}
const details: Record<string, string> = { clash: 'Clash', v2ray: 'v2ray', head: 'HEAD', get: 'GET 回退', tcp: 'TCP', quic: 'QUIC' }
const total = (r: { bytes_in: number; bytes_out: number }) => r.bytes_in + r.bytes_out
const groupOf = (kind: string) => kinds[kind]?.group

const pad = (n: number) => String(n).padStart(2, '0')
interface Stack { label: string; detail: string; parts: Record<Group, number>; sum: number; nic: number }
function stacks(rows: SiteTrafficRow[], slots: Date[], key: (d: Date) => string, label: (d: Date) => string, detail: (d: Date) => string): Stack[] {
  const map = new Map<string, Stack>()
  for (const d of slots) map.set(key(d), { label: label(d), detail: detail(d), parts: { probe: 0, source: 0, serve: 0 }, sum: 0, nic: 0 })
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

// 近 24 小时分类明细
interface Line { kind: string; detail: string; label: string; hint: string; requests: number; failures: number; bytes_in: number; bytes_out: number; children: Line[] }
const lines = computed(() => {
  const map = new Map<string, Line>()
  for (const r of props.data.hourly) {
    if (r.kind === 'nic') continue
    const meta = kinds[r.kind] ?? { label: r.kind, hint: '' }
    let line = map.get(r.kind)
    if (!line) { line = { kind: r.kind, detail: '', label: meta.label, hint: meta.hint, requests: 0, failures: 0, bytes_in: 0, bytes_out: 0, children: [] }; map.set(r.kind, line) }
    line.requests += r.requests; line.failures += r.failures; line.bytes_in += r.bytes_in; line.bytes_out += r.bytes_out
    if (r.detail) {
      let child = line.children.find(c => c.detail === r.detail)
      if (!child) { child = { kind: r.kind, detail: r.detail, label: details[r.detail] ?? r.detail, hint: '', requests: 0, failures: 0, bytes_in: 0, bytes_out: 0, children: [] }; line.children.push(child) }
      child.requests += r.requests; child.failures += r.failures; child.bytes_in += r.bytes_in; child.bytes_out += r.bytes_out
    }
  }
  const out = [...map.values()].sort((a, b) => total(b) - total(a))
  for (const l of out) l.children.sort((a, b) => total(b) - total(a))
  return out
})
const counted = computed(() => lines.value.filter(l => groupOf(l.kind)).reduce((n, l) => n + total(l), 0))
const groupTotals = computed(() => groups.map(g => ({ ...g, bytes: lines.value.filter(l => groupOf(l.kind) === g.key).reduce((n, l) => n + total(l), 0) })))
const nic24 = computed(() => props.data.hourly.filter(r => r.kind === 'nic').reduce((acc, r) => ({ bytes_in: acc.bytes_in + r.bytes_in, bytes_out: acc.bytes_out + r.bytes_out }), { bytes_in: 0, bytes_out: 0 }))
const expanded = ref<Set<string>>(new Set(['probe']))
function toggle(kind: string) { const s = new Set(expanded.value); if (s.has(kind)) s.delete(kind); else s.add(kind); expanded.value = s }
const share = (v: number) => counted.value > 0 ? `${(v / counted.value * 100).toFixed(v / counted.value >= 0.1 ? 0 : 1)}%` : '—'
const avg = (l: Line) => l.requests > 0 ? formatBytes(Math.round(total(l) / l.requests)) : '—'
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
    <div class="table-wrap site-traffic-table">
      <table>
        <thead><tr><th>近 24 小时分类</th><th>次数</th><th>接收</th><th>发送</th><th>合计</th><th>平均每次</th><th>占比</th></tr></thead>
        <tbody>
          <template v-for="l in lines" :key="l.kind">
            <tr :class="{ muted: !groupOf(l.kind) }">
              <td><button v-if="l.children.length" type="button" class="link-button expand" :aria-expanded="expanded.has(l.kind)" @click="toggle(l.kind)"><AppIcon name="down" />{{ l.label }}</button><strong v-else>{{ l.label }}</strong><small class="cell-sub">{{ l.hint }}</small></td>
              <td>{{ l.requests.toLocaleString() }}<small v-if="l.failures" class="cell-sub danger-text">失败 {{ l.failures.toLocaleString() }}</small></td>
              <td>{{ formatBytes(l.bytes_in) }}</td><td>{{ formatBytes(l.bytes_out) }}</td><td><strong>{{ formatBytes(total(l)) }}</strong></td><td>{{ avg(l) }}</td><td>{{ groupOf(l.kind) ? share(total(l)) : '不计入' }}</td>
            </tr>
            <template v-if="expanded.has(l.kind)">
              <tr v-for="c in l.children" :key="l.kind + c.detail" class="child">
                <td><span>{{ c.label }}</span></td>
                <td>{{ c.requests.toLocaleString() }}<small v-if="c.failures" class="cell-sub danger-text">失败 {{ c.failures.toLocaleString() }}</small></td>
                <td>{{ formatBytes(c.bytes_in) }}</td><td>{{ formatBytes(c.bytes_out) }}</td><td>{{ formatBytes(total(c)) }}</td><td>{{ avg(c) }}</td><td>{{ groupOf(c.kind) ? share(total(c)) : '' }}</td>
              </tr>
            </template>
          </template>
        </tbody>
      </table>
    </div>
    <p v-if="!lines.length" class="small muted">暂无记录。</p>
    <p class="small muted">字节在应用层统计，不含 TCP/IP 包头、重传和 DNS 查询，握手多的检测流量在网卡上会更高一些。订阅、网页按 Nginx 压缩前的大小计算，经压缩后出站更小。容器网卡包含 Nginx 与本服务之间、本服务与数据库之间的流量，可用来对照上面各项是否漏算。</p>
  </section>
</template>
