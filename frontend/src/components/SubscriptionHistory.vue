<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, useApi } from '../api/system'
import { useAccountScope, type HistoryItem, type RetainedPage } from '../community'
import PageNumbers from './PageNumbers.vue'
import AppIcon from './AppIcon.vue'
import '../community.css'
const props = defineProps<{ paused: boolean; refreshKey?: number }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const opened = ref(false)
const page = ref(1)
const data = ref<RetainedPage<HistoryItem> | null>(null)
const loading = ref(false)
const error = ref('')
const pageSize = 15
// 每种记录说明它对席位的影响
const actions: Record<string, { label: string; icon: string; tone: string }> = {
  claimed: { label: '开始使用，占用 1 个席位', icon: 'plus', tone: 'green' },
  activated: { label: '重新激活，占用席位', icon: 'check', tone: 'green' },
  renewed: { label: '席位续期', icon: 'refresh', tone: 'blue' },
  checkin: { label: '签到续期', icon: 'check', tone: 'blue' },
  probe: { label: '客户端自动续期', icon: 'probe', tone: 'blue' },
  leave: { label: '请假延期', icon: 'clock', tone: 'violet' },
  released: { label: '不再使用，释放席位', icon: 'unlink', tone: 'muted' },
  expired: { label: '租期到期，席位已释放', icon: 'clock', tone: 'amber' },
  removed: { label: '被提供方踢出，可重新加入', icon: 'unlink', tone: 'amber' },
  kicked: { label: '被提供方移出并禁用', icon: 'alert', tone: 'red' },
  unblocked: { label: '提供方解除禁用', icon: 'unlock', tone: 'green' },
}
const actionOf = (value: string) => actions[value] ?? { label: value, icon: 'info', tone: 'muted' }
// 按日期分组，同一天内连续的相同记录合并显示次数
const groups = computed(() => {
  const out: { day: string; items: (HistoryItem & { times: number })[] }[] = []
  for (const item of data.value?.items ?? []) {
    const day = formatDate(item.created_at).split(' ')[0]
    let group = out[out.length - 1]
    if (!group || group.day !== day) { group = { day, items: [] }; out.push(group) }
    const last = group.items[group.items.length - 1]
    if (last && last.source_id === item.source_id && last.action === item.action) last.times++
    else group.items.push({ ...item, times: 1 })
  }
  return out
})
const time = (value: string) => formatDate(value).split(' ')[1]?.slice(0, 5) ?? ''
let sequence = 0
let controller: AbortController | undefined
async function load() {
  if (!opened.value || props.paused) return
  const current = ++sequence, ticket = scope.ticket()
  controller?.abort(); controller = new AbortController(); loading.value = true
  try {
    const result = await api<RetainedPage<HistoryItem>>(`/subscriptions/history?page=${page.value}&page_size=${pageSize}`, { signal: controller.signal })
    if (current !== sequence || !scope.current(ticket)) return
    const last = Math.max(1, Math.ceil(result.total / pageSize))
    if (page.value > last) { page.value = last; return }
    data.value = result; error.value = ''
  } catch (cause) { if (current === sequence && scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { if (current === sequence) loading.value = false }
}
watch([opened, page, () => props.paused, () => props.refreshKey], () => { if (opened.value && !props.paused) void load(); else { ++sequence; controller?.abort(); loading.value = false } })
onBeforeUnmount(() => { ++sequence; controller?.abort() })
</script>
<template>
  <details class="panel subscription-history seat-history" :open="opened" @toggle="opened = ($event.target as HTMLDetailsElement).open">
    <summary><span class="history-summary-title"><AppIcon name="clock" />席位记录</span><small class="muted">每个共享来源的占用、续期与释放 · 保留 {{ data?.history_retention_days ?? 30 }} 天</small></summary>
    <div v-if="opened" class="history-body">
      <p class="seat-explain"><AppIcon name="info" /><span>订阅里只要包含某个来源的节点，就会占用这个来源的 1 个席位（同一来源多个节点也只占 1 个）。这里记录席位何时被占用、续期和释放，可以用来排查「某来源的节点为什么不见了」——通常是租期到期或被提供方移出。</span></p>
      <p v-if="loading && !data" class="small muted" role="status">正在读取记录…</p>
      <p v-if="error" class="notice error small" role="alert">{{ error }}<button :disabled="loading" @click="load">重试</button></p>
      <section v-for="group in groups" :key="group.day" class="seat-day">
        <h4>{{ group.day }}</h4>
        <ul class="seat-list">
          <li v-for="(item, index) in group.items" :key="`${item.created_at}-${index}`">
            <span :class="['seat-dot', `tone-${actionOf(item.action).tone}`]"><AppIcon :name="actionOf(item.action).icon" /></span>
            <span class="seat-text"><strong :title="item.source_name">{{ item.source_name || `来源 #${item.source_id}` }}</strong><small>{{ actionOf(item.action).label }}<template v-if="item.times > 1"> · {{ item.times }} 次</template></small></span>
            <time class="small muted">{{ time(item.created_at) }}</time>
          </li>
        </ul>
      </section>
      <p v-if="!loading && !error && !data?.items.length" class="small muted history-empty">暂无席位记录。</p>
      <div v-if="(data?.total ?? 0) > pageSize" class="compact-pagination"><span class="small muted">共 {{ data?.total ?? 0 }} 条</span><PageNumbers v-model="page" :pages="Math.max(1, Math.ceil((data?.total ?? 0) / pageSize))" :disabled="loading" previous-label="上一页记录" next-label="下一页记录" /></div>
    </div>
  </details>
</template>
