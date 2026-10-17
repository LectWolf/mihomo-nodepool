<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, useApi } from '../api/system'
import { useAccountScope, type DetailedSelection, type RetainedPage } from '../community'
import AppIcon from './AppIcon.vue'
import Modal from './Modal.vue'
import NodePreviewCards from './NodePreviewCards.vue'
import { navigate } from '../router'
import '../community.css'
interface Archive { id: number; subscription_id: number; name: string; nodes: DetailedSelection[]; source_ids: number[]; node_details: DetailedSelection[]; reason: string; archived_at: string }
const props = defineProps<{ paused: boolean; refreshKey?: number }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const opened = ref(false)
const page = ref(1)
const data = ref<RetainedPage<Archive> | null>(null)
const loading = ref(false)
const error = ref('')
const viewing = ref<Archive | null>(null)
const pages = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / 10)))
const reason = (value: string) => ({ replaced: '覆盖前快照', deleted: '删除前快照', source_removed: '来源移除前快照' }[value] ?? value)
function viewSource(id: number) { viewing.value = null; navigate('nodes', new URLSearchParams({ source_id: String(id) })) }
let sequence = 0
let controller: AbortController | undefined
async function load() {
  if (!opened.value || props.paused) return
  const current = ++sequence, ticket = scope.ticket()
  controller?.abort(); controller = new AbortController(); loading.value = true
  try {
    const result = await api<RetainedPage<Archive>>(`/subscriptions/archives?page=${page.value}&page_size=10`, { signal: controller.signal })
    if (current !== sequence || !scope.current(ticket)) return
    const last = Math.max(1, Math.ceil(result.total / 10))
    if (page.value > last) { page.value = last; return }
    data.value = result; error.value = ''
  } catch (cause) { if (current === sequence && scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { if (current === sequence) loading.value = false }
}
watch([opened, page, () => props.paused, () => props.refreshKey], () => {
  if (props.paused) { viewing.value = null; data.value = null }
  if (opened.value && !props.paused) void load()
  else { ++sequence; controller?.abort(); loading.value = false }
})
onBeforeUnmount(() => { ++sequence; controller?.abort() })
</script>
<template>
  <details class="panel subscription-history subscription-archives" :open="opened" @toggle="opened = ($event.target as HTMLDetailsElement).open">
    <summary>历史组合 <small class="muted">删除、覆盖和移除来源前的快照 · 保留 {{ data?.history_retention_days ?? 30 }} 天</small></summary>
    <div v-if="opened">
      <p class="small muted">保留当时的显示信息，历史快照不提供订阅链接或恢复席位。</p>
      <p v-if="loading" class="small muted" role="status">正在读取历史组合…</p>
      <p v-if="error" class="notice error small" role="alert">{{ error }}<button :disabled="loading" @click="load">重试</button></p>
      <div class="preview-node-grid"><article v-for="item in data?.items ?? []" :key="item.id" class="preview-node"><strong class="ellipsis" :title="item.name">{{ item.name }}</strong><span class="small muted">{{ reason(item.reason) }} · {{ item.nodes.length }} 节点 · {{ item.source_ids.length }} 来源</span><time class="small muted">{{ formatDate(item.archived_at) }}</time><button class="compact" @click="viewing = item">查看历史组合</button></article></div>
      <p v-if="!loading && !error && !data?.items.length" class="small muted">暂无历史组合。</p>
      <div class="compact-pagination"><span class="small muted">共 {{ data?.total ?? 0 }} 个快照</span><button class="icon-button" aria-label="上一页历史组合" :disabled="page <= 1 || loading" @click="page--"><AppIcon name="left" /></button><span>{{ page }} / {{ pages }}</span><button class="icon-button" aria-label="下一页历史组合" :disabled="page >= pages || loading" @click="page++"><AppIcon name="right" /></button></div>
    </div>
    <Modal v-if="viewing && !paused" title="历史组合详情" wide @close="viewing = null"><h3 class="ellipsis" :title="viewing.name">{{ viewing.name }}</h3><p class="small muted">{{ reason(viewing.reason) }} · {{ formatDate(viewing.archived_at) }}。以下为归档时信息。</p><NodePreviewCards :nodes="viewing.node_details || viewing.nodes" @source="viewSource" /><div class="dialog-actions"><button @click="viewing = null">关闭</button></div></Modal>
  </details>
</template>
