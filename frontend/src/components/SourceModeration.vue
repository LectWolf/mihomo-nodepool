<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, type Source } from '../api/system'
import { moderationApi, type ModerationPage } from '../api/moderation'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import UserAvatar from './UserAvatar.vue'
import PageNumbers from './PageNumbers.vue'
import TakedownDialog from './TakedownDialog.vue'
// 来源管理：查看提供方的全部来源（含已下架），并下架或恢复
const props = defineProps<{ ownerId?: number; ownerName?: string }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const status = ref('')
const q = ref('')
const page = ref(1)
const data = ref<ModerationPage | null>(null)
const loading = ref(false)
const error = ref('')
const target = ref<Source | null>(null)
const statuses = [{ value: '', label: '全部' }, { value: 'active', label: '上架中' }, { value: 'takedown', label: '已下架' }, { value: 'disabled', label: '提供方停用' }]
let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0
async function load() {
  const current = ++seq
  loading.value = true
  try {
    const params = new URLSearchParams({ page: String(page.value), page_size: '10' })
    if (props.ownerId) params.set('owner_id', String(props.ownerId))
    if (status.value) params.set('status', status.value)
    if (q.value.trim()) params.set('q', q.value.trim())
    const v = await moderationApi.list(params)
    if (current === seq) { data.value = v; error.value = '' }
  } catch (cause) { if (current === seq && !isCancelled(cause)) error.value = errorText(cause) }
  finally { if (current === seq) loading.value = false }
}
watch([status, q], () => { page.value = 1 })
watch([status, q, page], () => { clearTimeout(timer); timer = setTimeout(() => void load(), 200) }, { immediate: true })
onBeforeUnmount(() => { clearTimeout(timer); ++seq })
function done() { target.value = null; emit('changed'); void load() }
function stateOf(s: Source) { return s.takedown ? 'takedown' : s.enabled ? 'active' : 'disabled' }
const stateLabel: Record<string, string> = { takedown: '已下架', active: '上架中', disabled: '提供方停用' }
</script>

<template>
  <Modal :title="ownerName ? `${ownerName} 的来源` : '来源管理'" wide @close="emit('close')">
    <div class="moderation">
      <div class="moderation-toolbar">
        <div class="scope-tabs" role="group" aria-label="来源状态"><button v-for="s in statuses" :key="s.value" type="button" :aria-pressed="status === s.value" @click="status = s.value">{{ s.label }}<span v-if="s.value === 'takedown' && data?.takedowns && !status" class="count">{{ data.takedowns }}</span></button></div>
        <label class="search-field"><span class="sr-only">搜索来源</span><AppIcon name="search" /><input v-model="q" type="search" :placeholder="ownerId ? '搜索来源名称' : '搜索来源或提供方'" /></label>
      </div>
      <p v-if="error" class="notice error" role="alert">{{ error }}<button @click="load">重试</button></p>
      <ul class="moderation-list" :aria-busy="loading">
        <li v-for="s in data?.items ?? []" :key="s.id" :data-state="stateOf(s)">
          <div class="moderation-main">
            <UserAvatar :id="s.owner_id" :name="s.provider_name || s.provider_username || '管理员'" :version="s.provider_avatar" :size="34" />
            <div class="moderation-names"><strong :title="s.name">{{ s.name }}</strong><span class="small muted">{{ s.provider_name || s.provider_username }}<template v-if="s.provider_username"> · @{{ s.provider_username }}</template> · {{ s.node_count }} 个节点 · 席位 {{ s.occupied_users ?? s.active_users ?? 0 }}/{{ s.max_users || '∞' }}</span></div>
            <span :class="['mini-tag', stateOf(s) === 'takedown' ? 'red' : stateOf(s) === 'active' ? 'green' : '']">{{ stateLabel[stateOf(s)] }}</span>
          </div>
          <div class="moderation-actions"><button v-if="s.takedown" type="button" class="compact" @click="target = s"><AppIcon name="unlock" />恢复</button><button v-else type="button" class="compact danger" @click="target = s"><AppIcon name="lock" />下架</button></div>
          <p v-if="s.takedown" class="moderation-reason"><strong>{{ s.takedown.reason_label }}</strong><template v-if="s.takedown.detail">：{{ s.takedown.detail }}</template><small>{{ s.takedown.by }} · {{ formatDate(s.takedown.at) }}</small></p>
        </li>
      </ul>
      <div v-if="!loading && !data?.items.length" class="empty-state compact-empty"><AppIcon name="sources" /><p>{{ status === 'takedown' ? '没有已下架的来源' : '没有符合条件的来源' }}</p></div>
      <div class="pagination"><span class="small muted">{{ loading ? '正在读取…' : `共 ${data?.total ?? 0} 个来源` }}</span><PageNumbers v-model="page" :pages="Math.max(1, Math.ceil((data?.total ?? 0) / 10))" :disabled="loading" previous-label="上一页来源" next-label="下一页来源" /></div>
    </div>
    <TakedownDialog v-if="target" :source="target" @close="target = null" @done="done" />
  </Modal>
</template>
