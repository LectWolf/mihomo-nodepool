<script setup lang="ts">
import { computed } from 'vue'
import { formatDate } from '../api/system'
import { formatBytes, type SubscriptionUsage } from '../community'
import '../community.css'
const props = defineProps<{ usage?: SubscriptionUsage | null; detail?: boolean }>()
const statusNames = { unknown: '套餐未知', normal: '套餐正常', low: '余量偏低', exhausted: '流量耗尽', expired: '套餐已到期', invalid: '套餐信息异常' }
const summary = computed(() => {
  const usage = props.usage
  if (!usage) return ''
  if (['expired', 'exhausted', 'invalid'].includes(usage.status)) return statusNames[usage.status]
  const values: string[] = []
  if (usage.remaining_bytes != null) values.push(`余 ${formatBytes(usage.remaining_bytes)}`)
  else if (usage.status === 'low') values.push('余量偏低')
  if (usage.expires_at) {
    const remaining = new Date(usage.expires_at).getTime() - Date.now()
    if (remaining <= 0) values.push('已到期')
    else if (remaining <= 3 * 86400000) values.push('即将到期')
  }
  return values.join(' · ') || (usage.status === 'unknown' ? '套餐未知' : '')
})
const title = computed(() => {
  const u = props.usage
  if (!u) return ''
  return `来源套餐：${statusNames[u.status]}${u.remaining_bytes != null ? `；剩余 ${u.remaining_bytes.toLocaleString()} B` : ''}${u.expires_at ? `；到期 ${formatDate(u.expires_at)}` : ''}。此数据属于整个来源，并非每节点流量。`
})
function precise(value: number) { return `${formatBytes(value)}（${value.toLocaleString()} B）` }
</script>
<template>
  <span v-if="!detail && summary" class="usage-hint" :class="{ 'insight-risk': usage && ['low', 'expired', 'exhausted', 'invalid'].includes(usage.status) }" :title="title">{{ summary }}</span>
  <div v-else-if="detail" class="usage-detail">
    <p class="small muted">来源套餐信息 · 属于整个来源，并非每节点独立流量。未提供的数值表示未知。</p>
    <dl v-if="usage" class="detail-list">
      <div><dt>套餐状态</dt><dd>{{ statusNames[usage.status] }}</dd></div>
      <div v-if="usage.upload_bytes != null"><dt>已上传</dt><dd>{{ precise(usage.upload_bytes) }}</dd></div>
      <div v-if="usage.download_bytes != null"><dt>已下载</dt><dd>{{ precise(usage.download_bytes) }}</dd></div>
      <div v-if="usage.total_bytes != null"><dt>套餐总量</dt><dd>{{ precise(usage.total_bytes) }}</dd></div>
      <div v-if="usage.remaining_bytes != null"><dt>套餐剩余</dt><dd>{{ precise(usage.remaining_bytes) }}</dd></div>
      <div><dt>到期时间</dt><dd>{{ usage.expires_at ? formatDate(usage.expires_at) : '未提供' }}</dd></div>
      <div><dt>信息更新</dt><dd>{{ usage.fetched_at ? formatDate(usage.fetched_at) : '未提供' }}</dd></div>
    </dl>
    <p v-else class="small muted">来源未提供套餐数据。</p>
  </div>
</template>
