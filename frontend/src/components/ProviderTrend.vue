<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { errorText, isCancelled, useApi } from '../api/system'
import { formatBytes } from '../community'
import AppIcon from './AppIcon.vue'
import BarChart from './BarChart.vue'
interface Point { day: string; users: number; nodes: number; healthy: number; bytes: number; sampled?: boolean }
const props = defineProps<{ paused: boolean; revision?: number }>()
const api = useApi()
const days = ref(30)
const metric = ref<'users' | 'health' | 'bytes'>('users')
const points = ref<Point[]>([])
const loading = ref(false)
const error = ref('')
let sequence = 0
async function load() {
  if (props.paused) return
  const current = ++sequence
  loading.value = true
  try {
    const result = await api<{ days: Point[] }>(`/me/provider-trend?days=${days.value}`)
    if (current === sequence) { points.value = result.days ?? []; error.value = '' }
  } catch (cause) { if (current === sequence && !isCancelled(cause)) error.value = errorText(cause) }
  finally { if (current === sequence) loading.value = false }
}
watch([days, () => props.paused, () => props.revision], () => { void load() }, { immediate: true })
const label = (p: Point) => { const d = new Date(p.day); return `${d.getMonth() + 1}/${d.getDate()}` }
const detail = (p: Point) => { const d = new Date(p.day); return `${d.getMonth() + 1}月${d.getDate()}日` }
const health = (p: Point) => p.nodes > 0 ? Math.round(p.healthy / p.nodes * 100) : 0
const bars = computed(() => points.value.map(p => ({ label: label(p), detail: detail(p), value: metric.value === 'users' ? p.users : metric.value === 'health' ? health(p) : p.bytes, missing: metric.value === 'bytes' ? false : metric.value === 'health' ? !p.sampled || !p.nodes : p.sampled === false })))
const format = computed(() => metric.value === 'bytes' ? (v: number) => formatBytes(v) : metric.value === 'health' ? (v: number) => `${Math.round(v)}%` : (v: number) => `${Math.round(v)} 人`)
const latest = computed(() => [...points.value].reverse().find(p => p.nodes > 0 || p.users > 0))
const totalBytes = computed(() => points.value.reduce((n, p) => n + p.bytes, 0))
const peakUsers = computed(() => Math.max(0, ...points.value.map(p => p.users)))
const metrics = [{ id: 'users', label: '使用人数' }, { id: 'health', label: '健康率' }, { id: 'bytes', label: '流量' }] as const
</script>

<template>
  <section class="panel provider-trend">
    <div class="panel-heading"><div><h2><AppIcon name="chart" />我的共享趋势</h2><p class="small muted">按天汇总你提供的全部来源：使用人数与健康率取当天最后一次整点快照，流量为套餐已用量的增量。</p></div>
      <div class="trend-controls"><div class="scope-tabs" role="group" aria-label="趋势指标"><button v-for="m in metrics" :key="m.id" type="button" :aria-pressed="metric === m.id" @click="metric = m.id">{{ m.label }}</button></div><div class="scope-tabs" role="group" aria-label="时间范围"><button type="button" :aria-pressed="days === 7" @click="days = 7">7 天</button><button type="button" :aria-pressed="days === 30" @click="days = 30">30 天</button></div></div>
    </div>
    <div class="trend-stats"><div><span>当前使用</span><strong>{{ latest?.users ?? 0 }}<small> 人</small></strong></div><div><span>峰值使用</span><strong>{{ peakUsers }}<small> 人</small></strong></div><div><span>当前健康率</span><strong>{{ latest ? health(latest) : 0 }}<small>%</small></strong></div><div><span>{{ days }} 天流量</span><strong>{{ formatBytes(totalBytes) }}</strong></div></div>
    <p v-if="error" class="notice error small" role="alert">{{ error }}<button type="button" @click="load">重试</button></p>
    <BarChart :bars="bars" :format="format" :tone="metric === 'users' ? 'green' : metric === 'health' ? 'amber' : 'violet'" :every="days === 7 ? 1 : 5" :height="140" :bytes="metric === 'bytes'" :aggregate="metric === 'bytes' ? 'sum' : 'avg'" :empty="loading ? '正在读取…' : '整点快照后开始记录趋势'" />
  </section>
</template>
