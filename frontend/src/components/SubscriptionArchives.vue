<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi } from '../api/system'
import { loadProfile, type FollowSource, type Subscription } from '../api/accounts'
import { useAccountScope, type DetailedSelection, type RetainedPage } from '../community'
import { toast } from '../toast'
import PageNumbers from './PageNumbers.vue'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import CustomSelect from './CustomSelect.vue'
import NodePreviewCards from './NodePreviewCards.vue'
import { navigate } from '../router'
import '../community.css'
interface Archive { id: number; subscription_id: number; name: string; nodes: DetailedSelection[]; source_ids: number[]; follow_details?: FollowSource[]; node_details: DetailedSelection[]; reason: string; archived_at: string }
const props = defineProps<{ paused: boolean; refreshKey?: number; subscriptions?: Subscription[]; limit?: number }>()
const emit = defineEmits<{ restored: [] }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const opened = ref(false)
const page = ref(1)
const data = ref<RetainedPage<Archive> | null>(null)
const loading = ref(false)
const error = ref('')
const viewing = ref<Archive | null>(null)
const restoring = ref<Archive | null>(null)
const target = ref<number | string>('')
const busy = ref(false)
const restoreError = ref('')
const pageSize = 10
const pages = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / pageSize)))
const reasons: Record<string, { label: string; icon: string; tone: string }> = {
  replaced: { label: '修改前', icon: 'edit', tone: 'blue' },
  deleted: { label: '删除前', icon: 'trash', tone: 'red' },
  source_removed: { label: '来源移除前', icon: 'unlink', tone: 'amber' },
}
const reasonOf = (value: string) => reasons[value] ?? { label: value, icon: 'history', tone: 'blue' }
const full = computed(() => (props.subscriptions?.length ?? 0) >= (props.limit ?? Infinity))
const targetOptions = computed(() => [
  ...(full.value ? [] : [{ value: '' as number | string, label: '恢复为新订阅' }]),
  ...(props.subscriptions ?? []).map(item => ({ value: item.id as number | string, label: `覆盖 · ${item.name}` })),
])
function available(item: Archive) { return (item.node_details || item.nodes).filter(node => node.available !== false).length + follows(item).filter(s => s.available).length }
function follows(item: Archive) { return item.follow_details ?? [] }
function brokenNodes(item: Archive) { return (item.node_details || item.nodes).filter(node => node.available === false).length }
function viewSource(id: number) { viewing.value = null; navigate('nodes', new URLSearchParams({ source_id: String(id) })) }
function openRestore(item: Archive) {
  restoring.value = item; restoreError.value = ''; viewing.value = null
  const original = props.subscriptions?.find(sub => sub.id === item.subscription_id)
  target.value = original ? original.id : full.value ? (props.subscriptions?.[0]?.id ?? '') : ''
}
async function restore() {
  const item = restoring.value
  if (!item || busy.value || props.paused) return
  const ticket = scope.ticket()
  busy.value = true; restoreError.value = ''
  try {
    const result = await api<{ skipped: number; selected_count: number }>(`/subscriptions/archives/${item.id}/restore`, json('POST', target.value === '' ? {} : { subscription_id: Number(target.value) }))
    if (!scope.current(ticket)) return
    restoring.value = null
    toast(result.skipped ? `已恢复「${item.name}」，${result.selected_count} 个节点；${result.skipped} 个已失效节点被跳过` : `已恢复「${item.name}」，共 ${result.selected_count} 个节点`)
    emit('restored'); void load(); void loadProfile().catch(() => undefined)
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) restoreError.value = errorText(cause) }
  finally { busy.value = false }
}
let sequence = 0
let controller: AbortController | undefined
async function load() {
  if (!opened.value || props.paused) return
  const current = ++sequence, ticket = scope.ticket()
  controller?.abort(); controller = new AbortController(); loading.value = true
  try {
    const result = await api<RetainedPage<Archive>>(`/subscriptions/archives?page=${page.value}&page_size=${pageSize}`, { signal: controller.signal })
    if (current !== sequence || !scope.current(ticket)) return
    const last = Math.max(1, Math.ceil(result.total / pageSize))
    if (page.value > last) { page.value = last; return }
    data.value = result; error.value = ''
  } catch (cause) { if (current === sequence && scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { if (current === sequence) loading.value = false }
}
watch([opened, page, () => props.paused, () => props.refreshKey], () => {
  if (props.paused) { viewing.value = null; restoring.value = null; data.value = null }
  if (opened.value && !props.paused) void load()
  else { ++sequence; controller?.abort(); loading.value = false }
})
onBeforeUnmount(() => { ++sequence; controller?.abort() })
</script>
<template>
  <details class="panel subscription-history subscription-archives" :open="opened" @toggle="opened = ($event.target as HTMLDetailsElement).open">
    <summary><span class="history-summary-title"><AppIcon name="history" />历史组合</span><small class="muted">修改、删除前自动保存 · 保留最近 10 次，可一键恢复</small></summary>
    <div v-if="opened" class="history-body">
      <p v-if="loading && !data" class="small muted" role="status">正在读取历史组合…</p>
      <p v-if="error" class="notice error small" role="alert">{{ error }}<button :disabled="loading" @click="load">重试</button></p>
      <ul v-if="data?.items.length" class="archive-list">
        <li v-for="item in data.items" :key="item.id" class="archive-item">
          <span :class="['archive-badge', `tone-${reasonOf(item.reason).tone}`]"><AppIcon :name="reasonOf(item.reason).icon" /></span>
          <div class="archive-main">
            <strong class="ellipsis" :title="item.name">{{ item.name }}</strong>
            <span class="small muted">{{ reasonOf(item.reason).label }}的组合 · {{ item.nodes.length }} 个节点 · {{ item.source_ids.length }} 个来源<template v-if="follows(item).length"> · 跟随 {{ follows(item).length }} 个来源</template><template v-if="brokenNodes(item)"> · <b class="amber-text">{{ brokenNodes(item) }} 个已失效</b></template></span>
          </div>
          <time class="small muted">{{ formatDate(item.archived_at) }}</time>
          <div class="archive-actions"><button class="compact" @click="viewing = item"><AppIcon name="search" />查看</button><button class="compact primary" :disabled="paused || !available(item)" :title="available(item) ? '用这次的节点组合恢复订阅' : '节点均已失效，无法恢复'" @click="openRestore(item)"><AppIcon name="reset" />恢复</button></div>
        </li>
      </ul>
      <p v-if="!loading && !error && !data?.items.length" class="small muted history-empty">还没有历史组合。修改或删除订阅时，会自动保存之前的节点组合。</p>
      <div v-if="pages > 1" class="compact-pagination"><span class="small muted">共 {{ data?.total ?? 0 }} 个快照</span><PageNumbers v-model="page" :pages="pages" :disabled="loading" previous-label="上一页历史组合" next-label="下一页历史组合" /></div>
    </div>
    <Modal v-if="viewing && !paused" title="历史组合详情" wide @close="viewing = null"><h3 class="ellipsis" :title="viewing.name">{{ viewing.name }}</h3><p class="small muted">{{ reasonOf(viewing.reason).label }}保存 · {{ formatDate(viewing.archived_at) }}。以下为保存时的节点信息。</p><p v-if="follows(viewing).length" class="small">跟随来源：<template v-for="(s, i) in follows(viewing)" :key="s.source_id"><template v-if="i">、</template><span :class="s.available ? '' : 'amber-text'">{{ s.name }}{{ s.available ? '' : '（不可用）' }}</span></template></p><NodePreviewCards v-if="viewing.nodes.length" :nodes="viewing.node_details || viewing.nodes" @source="viewSource" /><div class="dialog-actions"><button @click="viewing = null">关闭</button><button class="primary" :disabled="!available(viewing)" @click="openRestore(viewing)"><AppIcon name="reset" />恢复这个组合</button></div></Modal>
    <Modal v-if="restoring && !paused" title="恢复历史组合" :busy="busy" @close="restoring = null">
      <p>把「{{ restoring.name }}」的 {{ restoring.nodes.length }} 个节点<template v-if="follows(restoring).length">和 {{ follows(restoring).length }} 个跟随来源</template>恢复到订阅。</p>
      <div class="form-field"><span class="field-label">恢复到</span><CustomSelect v-model="target" label="恢复到" :options="targetOptions" :disabled="busy" /><span class="field-hint">{{ target === '' ? '新建一条订阅，会生成新的订阅链接。' : '覆盖所选订阅的节点和规则，订阅链接保持不变；被覆盖的组合也会存入历史。' }}</span></div>
      <p v-if="brokenNodes(restoring) || follows(restoring).some(s => !s.available)" class="notice warning">已失效或被停用的节点和来源会在恢复时自动跳过。</p>
      <p v-if="full && !subscriptions?.length" class="notice info">订阅数量已满，请先删除一条订阅。</p>
      <p v-if="restoreError" class="notice error" role="alert">{{ restoreError }}</p>
      <div class="dialog-actions"><button :disabled="busy" @click="restoring = null">取消</button><button class="primary" :disabled="busy || (full && target === '')" @click="restore">{{ busy ? '正在恢复…' : '确认恢复' }}</button></div>
    </Modal>
  </details>
</template>
