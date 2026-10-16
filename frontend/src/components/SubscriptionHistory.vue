<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, useApi } from '../api/system'
import { historyAction, useAccountScope, type HistoryItem, type RetainedPage } from '../community'
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
let sequence = 0
let controller: AbortController | undefined
async function load() {
  if (!opened.value || props.paused) return
  const current = ++sequence, ticket = scope.ticket()
  controller?.abort(); controller = new AbortController(); loading.value = true
  try {
    const result = await api<RetainedPage<HistoryItem>>(`/subscriptions/history?page=${page.value}&page_size=10`, { signal: controller.signal })
    if (current !== sequence || !scope.current(ticket)) return
    const last = Math.max(1, Math.ceil(result.total / 10))
    if (page.value > last) { page.value = last; return }
    data.value = result; error.value = ''
  } catch (cause) { if (current === sequence && scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { if (current === sequence) loading.value = false }
}
watch([opened, page, () => props.paused, () => props.refreshKey], () => { if (opened.value && !props.paused) void load(); else { ++sequence; controller?.abort(); loading.value = false } })
onBeforeUnmount(() => { ++sequence; controller?.abort() })
</script>
<template>
  <details class="panel subscription-history" :open="opened" @toggle="opened = ($event.target as HTMLDetailsElement).open">
    <summary>订阅历史 <small class="muted">来源领取、续期与释放记录 · 保留 {{ data?.history_retention_days ?? 30 }} 天</small></summary>
    <div v-if="opened">
      <p v-if="loading" class="small muted" role="status">正在读取历史…</p>
      <p v-if="error" class="notice error small" role="alert">{{ error }}<button :disabled="loading" @click="load">重试</button></p>
      <ul class="community-history-list"><li v-for="(item, index) in data?.items ?? []" :key="`${item.created_at}-${index}`"><span><strong :title="item.source_name">{{ item.source_name || `来源 #${item.source_id}` }}</strong><small class="muted">{{ historyAction(item.action) }}</small></span><time class="small muted">{{ formatDate(item.created_at) }}</time></li></ul>
      <p v-if="!loading && !error && !data?.items.length" class="small muted">暂无订阅历史。</p>
      <div class="compact-pagination"><span class="small muted">共 {{ data?.total ?? 0 }} 条</span><button class="icon-button" aria-label="上一页历史" :disabled="page <= 1 || loading" @click="page--"><AppIcon name="left" /></button><span>{{ page }} / {{ Math.max(1, Math.ceil((data?.total ?? 0) / 10)) }}</span><button class="icon-button" aria-label="下一页历史" :disabled="loading || page * 10 >= (data?.total ?? 0)" @click="page++"><AppIcon name="right" /></button></div>
    </div>
  </details>
</template>
