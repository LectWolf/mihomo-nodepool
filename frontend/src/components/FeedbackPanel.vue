<script setup lang="ts">
import { toastRef } from '../toast'
import { computed, ref, watch } from 'vue'
import { errorText, isCancelled } from '../api/system'
import { feedbackApi, feedbackKinds, feedbackLabel, feedbackTone, type FeedbackPage } from '../api/economy'
import { timeAgo } from '../community'
import AppIcon from './AppIcon.vue'
import PageNumbers from './PageNumbers.vue'
import UserAvatar from './UserAvatar.vue'
const props = defineProps<{ target: 'node' | 'source' | 'all'; nodeId?: number; sourceId?: number; origins?: { id: number; name: string }[]; canSubmit?: boolean; startOpen?: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const data = ref<FeedbackPage | null>(null)
const page = ref(1)
const loading = ref(false)
const error = ref('')
const message = toastRef()
const formOpen = ref(!!props.startOpen)
const kind = ref('')
const comment = ref('')
const origin = ref(0)
const busy = ref(false)
const submitTarget = computed<'node' | 'source'>(() => props.target === 'node' ? 'node' : 'source')
const kinds = computed(() => feedbackKinds[submitTarget.value])
const pageSize = 5
watch(() => props.origins, list => { if (!list?.some(o => o.id === origin.value)) origin.value = list?.[0]?.id ?? props.sourceId ?? 0 }, { immediate: true })
async function load() {
  loading.value = true
  try {
    const params: Record<string, string | number> = { target: props.target, page: page.value, page_size: pageSize }
    if (props.nodeId) params.node_id = props.nodeId
    if (props.sourceId) params.source_id = props.sourceId
    data.value = await feedbackApi.list(params); error.value = ''
  } catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { loading.value = false }
}
watch(() => [props.target, props.nodeId, props.sourceId, page.value], () => void load(), { immediate: true })
async function submit() {
  if (!kind.value || busy.value) return
  busy.value = true; error.value = ''; message.value = ''
  try {
    const source = props.target === 'node' ? origin.value : props.sourceId ?? 0
    await feedbackApi.submit({ target: submitTarget.value, ...(props.target === 'node' ? { node_id: props.nodeId } : {}), source_id: source, kind: kind.value, comment: comment.value.trim() })
    message.value = '反馈已提交，24 小时内再次提交会覆盖这一条。'; formOpen.value = false; kind.value = ''; comment.value = ''; page.value = 1
    await load(); emit('changed')
  } catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
async function resolve(id: number, resolved: boolean) {
  try { await feedbackApi.resolve(id, resolved); await load(); emit('changed') } catch (cause) { error.value = errorText(cause) }
}
async function remove(id: number) {
  try { await feedbackApi.remove(id); await load(); emit('changed') } catch (cause) { error.value = errorText(cause) }
}
function probeText(item: FeedbackPage['items'][number]) {
  if (item.target === 'source') return item.probe_status ? `反馈时 ${item.probe_status.replace('/', ' / ')} 个节点健康` : ''
  const labels: Record<string, string> = { healthy: '健康', unhealthy: '不可用', unknown: '待检测' }
  return `反馈时平台检测：${labels[item.probe_status] ?? '未知'}${item.probe_latency != null ? ` · ${item.probe_latency} ms` : ''}`
}
</script>
<template>
  <section class="feedback-panel">
    <div class="feedback-head">
      <div><h3>用户反馈 <span class="count">{{ data?.total ?? 0 }}</span></h3><p class="small muted">展示近 {{ data?.visible_days ?? 30 }} 天，来自正在使用的用户，身份匿名为等级。</p></div>
      <button v-if="canSubmit !== false && target !== 'all'" type="button" class="compact" :aria-expanded="formOpen" @click="formOpen = !formOpen"><AppIcon :name="formOpen ? 'close' : 'edit'" />{{ formOpen ? '收起' : '我要反馈' }}</button>
    </div>
    <form v-if="formOpen" class="feedback-form" @submit.prevent="submit">
      <div class="feedback-kinds" role="radiogroup" aria-label="反馈类型"><button v-for="item in kinds" :key="item.value" type="button" role="radio" :aria-checked="kind === item.value" :data-tone="item.tone" @click="kind = item.value">{{ item.label }}</button></div>
      <div v-if="target === 'node' && (origins?.length ?? 0) > 1" class="feedback-origins"><span class="small muted">反馈的来源</span><button v-for="o in origins" :key="o.id" type="button" class="chip-button" :aria-pressed="origin === o.id" @click="origin = o.id">{{ o.name }}</button></div>
      <label class="form-field"><span class="field-label">补充说明 <small class="muted">可选 · {{ comment.length }}/200</small></span><textarea v-model="comment" rows="2" maxlength="200" placeholder="例如：晚高峰无法连接、仅部分网站慢"></textarea></label>
      <div class="feedback-form-actions"><span class="small muted">仅订阅中包含该节点或正在使用该来源的用户可以提交；提供方会看到你的用户名。问题被提供方确认后可获得经验和积分（每日有上限），多人反馈的节点会在订阅中排到最后。</span><button class="primary compact" :disabled="!kind || busy">{{ busy ? '提交中…' : '提交反馈' }}</button></div>
    </form>

    <p v-if="error" class="notice error small" role="alert">{{ error }}</p>
    <ul v-if="data?.items.length" class="feedback-list">
      <li v-for="item in data.items" :key="item.id" :class="{ resolved: !!item.resolved_at }">
        <UserAvatar :id="item.reporter_id" :name="item.reporter" :version="item.reporter_avatar" :size="28" />
        <div class="feedback-body">
          <div class="feedback-line"><span :class="['feedback-kind', feedbackTone(item.target, item.kind)]">{{ feedbackLabel(item.target, item.kind) }}</span><strong>{{ item.reporter }}</strong><span class="small muted" :title="new Date(item.created_at).toLocaleString('zh-CN', { hour12: false })">{{ timeAgo(item.created_at) }}</span><span v-if="target === 'all'" class="small muted">· {{ item.target === 'node' ? item.node_name : '整个来源' }}</span></div>
          <p v-if="item.comment" class="feedback-comment">{{ item.comment }}</p>
          <div class="feedback-meta"><span>已使用 {{ item.usage_days }} 天</span><span>{{ probeText(item) }}</span><span v-if="item.checked_ok_after" class="green-text"><AppIcon name="check" />反馈后平台检测已成功</span><span v-if="item.resolved_at" class="muted">提供方已处理</span></div>
        </div>
        <div class="feedback-actions">
          <button v-if="item.can_resolve" type="button" class="text-button" @click="resolve(item.id, !item.resolved_at)">{{ item.resolved_at ? '撤销处理' : '标记已处理' }}</button>
          <button v-if="item.can_delete" type="button" class="text-button danger" @click="remove(item.id)">删除</button>
        </div>
      </li>
    </ul>
    <p v-else-if="!loading && !error" class="small muted feedback-empty">暂无反馈。</p>
    <PageNumbers v-if="(data?.total ?? 0) > pageSize" v-model="page" :pages="Math.ceil((data?.total ?? 0) / pageSize)" :disabled="loading" label="反馈分页" />
  </section>
</template>
