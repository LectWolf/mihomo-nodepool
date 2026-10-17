<script setup lang="ts">
import { computed } from 'vue'
import { formatDate } from '../api/system'
import { daysUntil, formatBytes, type SubscriptionUsage } from '../community'
import '../community.css'
const props = defineProps<{ usage?: SubscriptionUsage | null; detail?: boolean; bar?: boolean }>()
const statusNames = { unknown: '套餐未知', normal: '套餐正常', low: '余量偏低', exhausted: '流量耗尽', expired: '套餐已到期', invalid: '套餐信息异常' }
const risky = computed(() => !!props.usage && ['low', 'expired', 'exhausted', 'invalid'].includes(props.usage.status))
const days = computed(() => daysUntil(props.usage?.expires_at))
const expiryText = computed(() => {
  if (days.value === null) return ''
  if (days.value <= 0) return '已到期'
  return days.value <= 3 ? `${days.value} 天后到期 · 即将到期` : `${days.value} 天后到期`
})
const summary = computed(() => {
  const usage = props.usage
  if (!usage) return ''
  if (['expired', 'exhausted', 'invalid'].includes(usage.status)) return statusNames[usage.status]
  const values: string[] = []
  if (usage.remaining_bytes != null) values.push(`余 ${formatBytes(usage.remaining_bytes)}`)
  else if (usage.status === 'low') values.push('余量偏低')
  if (days.value !== null && days.value <= 3) values.push(days.value <= 0 ? '已到期' : '即将到期')
  return values.join(' · ') || (usage.status === 'unknown' ? '套餐未知' : '')
})
// 已知总量时用剩余比例画流量条
const meter = computed(() => {
  const u = props.usage
  if (!u || u.total_bytes == null || u.total_bytes <= 0) return null
  const remaining = u.remaining_bytes ?? Math.max(0, u.total_bytes - (u.upload_bytes ?? 0) - (u.download_bytes ?? 0))
  const percent = Math.max(0, Math.min(100, remaining / u.total_bytes * 100))
  return { remaining, total: u.total_bytes, used: Math.max(0, u.total_bytes - remaining), percent, tone: percent <= 10 || ['expired', 'exhausted', 'invalid'].includes(u.status) ? 'bad' : percent <= 30 || u.status === 'low' ? 'warn' : 'good' }
})
const title = computed(() => {
  const u = props.usage
  if (!u) return ''
  return `来源套餐：${statusNames[u.status]}${u.remaining_bytes != null ? `；剩余 ${u.remaining_bytes.toLocaleString()} B` : ''}${u.expires_at ? `；到期 ${formatDate(u.expires_at)}` : ''}。此数据属于整个来源，并非每节点流量。`
})
</script>
<template>
  <div v-if="!detail && bar && meter" class="usage-meter" :title="title">
    <div class="usage-meter-top"><span>剩余 <b>{{ formatBytes(meter.remaining) }}</b><small> / {{ formatBytes(meter.total) }}</small></span><span v-if="expiryText" :class="{ 'danger-text': days !== null && days <= 3 }">{{ expiryText }}</span></div>
    <div class="usage-bar" :data-tone="meter.tone" role="meter" :aria-label="`套餐剩余 ${Math.round(meter.percent)}%`" :aria-valuenow="Math.round(meter.percent)" aria-valuemin="0" aria-valuemax="100"><span :style="{ width: `${meter.percent}%` }"></span></div>
  </div>
  <span v-else-if="!detail && summary" class="usage-hint" :class="{ 'insight-risk': risky }" :title="title">{{ summary }}<template v-if="bar && expiryText && !summary.includes('到期')"> · {{ expiryText }}</template></span>
  <div v-else-if="detail" class="usage-detail">
    <template v-if="usage">
      <div class="usage-detail-head"><span :class="['badge', risky ? 'error' : usage.status === 'normal' ? 'ready' : 'queued']">{{ statusNames[usage.status] }}</span><span class="small muted">{{ usage.fetched_at ? `更新于 ${formatDate(usage.fetched_at)}` : '更新时间未提供' }}</span></div>
      <div v-if="meter" class="usage-meter large">
        <div class="usage-meter-top"><span>剩余 <b>{{ formatBytes(meter.remaining) }}</b><small> / 总量 {{ formatBytes(meter.total) }}</small></span><span>{{ Math.round(meter.percent) }}%</span></div>
        <div class="usage-bar" :data-tone="meter.tone"><span :style="{ width: `${meter.percent}%` }"></span></div>
      </div>
      <div class="usage-tiles">
        <div :title="usage.upload_bytes != null ? `${usage.upload_bytes.toLocaleString()} B` : ''"><span>已上传</span><strong>{{ usage.upload_bytes != null ? formatBytes(usage.upload_bytes) : '未知' }}</strong></div>
        <div :title="usage.download_bytes != null ? `${usage.download_bytes.toLocaleString()} B` : ''"><span>已下载</span><strong>{{ usage.download_bytes != null ? formatBytes(usage.download_bytes) : '未知' }}</strong></div>
        <div :title="usage.remaining_bytes != null ? `${usage.remaining_bytes.toLocaleString()} B` : ''"><span>剩余</span><strong>{{ usage.remaining_bytes != null ? formatBytes(usage.remaining_bytes) : '未知' }}</strong></div>
        <div><span>到期</span><strong>{{ usage.expires_at ? formatDate(usage.expires_at).split(' ')[0] : '未提供' }}</strong><small v-if="expiryText" :class="{ 'danger-text': days !== null && days <= 3 }">{{ expiryText }}</small></div>
      </div>
      <p class="small muted">套餐信息属于整个来源，并非每个节点独立流量。</p>
    </template>
    <p v-else class="small muted">来源未提供套餐数据。</p>
  </div>
</template>
