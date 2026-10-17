<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { currentUser } from '../api/accounts'
import { errorText, formatDate, isCancelled, json, useApi, type Source } from '../api/system'
import { levelLabel, useAccountScope, type RetainedPage, type Subscriber } from '../community'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import '../community.css'
const props = defineProps<{ source: Source }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const api = useApi()
const scope = useAccountScope()
const state = ref<'current' | 'history' | 'all'>('current')
const tabs = [{ value: 'current', label: '当前在订' }, { value: 'history', label: '历史记录' }, { value: 'all', label: '全部' }] as const
const page = ref(1)
const data = ref<RetainedPage<Subscriber> | null>(null)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const message = ref('')
const kicking = ref<Subscriber | null>(null)
const states = { active: '当前在订', expired: '租期已到期', released: '已释放', kicked: '已踢出 · 禁止领取' }
const pages = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / 20)))
let sequence = 0
async function load() {
  const current = ++sequence, ticket = scope.ticket()
  loading.value = true
  try {
    const result = await api<RetainedPage<Subscriber>>(`/sources/${props.source.id}/subscribers?state=${state.value}&page=${page.value}&page_size=20`)
    if (current !== sequence || !scope.current(ticket)) return
    const last = Math.max(1, Math.ceil(result.total / 20))
    if (page.value > last) { page.value = last; return }
    data.value = result; error.value = ''
  } catch (cause) { if (current === sequence && scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { if (current === sequence) loading.value = false }
}
watch(state, () => { page.value = 1; kicking.value = null; data.value = null }, { flush: 'sync' })
watch([state, page], () => void load(), { immediate: true })
function canManage(user: Subscriber) { return user.user_id !== props.source.owner_id && (currentUser.value?.role === 'admin' || props.source.owner_id === currentUser.value?.id) }
function renewal(method: string) { return ({ checkin: '网页签到', web: '网页激活', probe: '有效探测', created: '首次领取' } as Record<string, string>)[method] || method || '未记录' }
async function block(user: Subscriber, blocked: boolean) {
  if (busy.value || !canManage(user) || (blocked && kicking.value?.user_id !== user.user_id)) return
  const ticket = scope.ticket()
  busy.value = true; error.value = ''; message.value = ''
  try {
    await api(`/sources/${props.source.id}/subscribers/${user.user_id}`, json('PATCH', { blocked }))
    if (!scope.current(ticket)) return
    kicking.value = null; message.value = blocked ? '已从该用户全部组合中移除此来源，并禁止再次领取。' : '已解除此来源的领取禁用，用户需要重新选择并激活。'
    emit('changed'); await load()
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
</script>
<template>
  <Modal title="订阅用户" wide :busy="busy" @close="emit('close')">
    <h3 class="ellipsis" :title="source.name">{{ source.name }}</h3>
    <p class="small muted">有效席位 {{ source.occupied_users ?? source.active_users ?? 0 }} / {{ source.max_users || '不限' }}。删除最后引用会释放席位；历史记录保留 {{ data?.history_retention_days ?? 30 }} 天。</p>
    <div class="tab-bar" role="tablist" aria-label="订阅用户范围"><button v-for="tab in tabs" :key="tab.value" role="tab" :aria-selected="state === tab.value" :class="{ active: state === tab.value }" :disabled="busy" @click="state = tab.value">{{ tab.label }}</button></div>
    <p v-if="message" class="notice success small" role="status">{{ message }}</p>
    <p v-if="error" class="notice error small" role="alert">{{ error }}<button :disabled="loading || busy" @click="load">重试</button></p>
    <p v-if="loading" class="small muted" role="status">正在读取{{ state === 'current' ? '当前订阅' : '订阅记录' }}…</p>
    <div :aria-busy="loading">
      <article v-for="user in data?.items ?? []" :key="user.user_id" class="subscriber-card">
        <div class="subscriber-heading"><div><strong :title="user.display_name || user.username">{{ user.display_name || user.username }}</strong><span class="small muted">{{ user.username }} · {{ levelLabel(user.level) }} · {{ user.xp }} XP<span v-if="!user.enabled"> · 账号停用</span></span></div><span class="badge" :class="user.state === 'active' ? 'ready' : user.state === 'kicked' ? 'error' : 'queued'">{{ states[user.state] }}</span></div>
        <p class="small muted">本来源引用：{{ user.node_count }} 个节点 · {{ user.subscription_count }} 个组合</p>
        <dl class="subscriber-times">
          <div><dt>首次领取</dt><dd>{{ formatDate(user.claimed_at) }}</dd></div><div><dt>租期到期</dt><dd>{{ formatDate(user.expires_at) }}</dd></div>
          <div><dt>最近拉取</dt><dd>{{ user.last_pull_at ? formatDate(user.last_pull_at) : '尚无记录' }}</dd></div><div><dt>最近续期 · {{ renewal(user.renew_method) }}</dt><dd>{{ user.last_renewed_at ? formatDate(user.last_renewed_at) : '尚无记录' }}</dd></div>
          <div v-if="user.ended_at"><dt>结束时间</dt><dd>{{ formatDate(user.ended_at) }}</dd></div>
        </dl>
        <div v-if="canManage(user)" class="source-community-actions"><button v-if="user.state === 'kicked'" class="compact" :disabled="busy" @click="block(user, false)">解除领取禁用</button><button v-else class="compact danger" :disabled="busy" @click="kicking = user; error = ''">踢出并禁止领取</button></div>
        <p v-else-if="user.user_id === source.owner_id" class="small muted">来源所有者不可踢出。</p>
      </article>
      <p v-if="!loading && !error && !data?.items.length" class="small muted">{{ state === 'current' ? '暂无当前在订用户。' : '暂无历史记录。' }}</p>
    </div>
    <div v-if="kicking" class="subscriber-confirm" role="alertdialog" aria-label="确认踢出订阅用户"><p>确定踢出「{{ kicking.display_name || kicking.username }}」并禁止再次领取吗？此来源会从其所有组合中移除，现有有效席位将释放。</p><div class="dialog-actions"><button :disabled="busy" @click="kicking = null">取消</button><button class="danger-solid" :disabled="busy" @click="block(kicking, true)">确认踢出并禁用</button></div></div>
    <div class="compact-pagination"><span class="small muted">共 {{ data?.total ?? 0 }} 人</span><button class="icon-button" aria-label="上一页订阅用户" :disabled="page <= 1 || loading || busy" @click="page--"><AppIcon name="left" /></button><span>{{ page }} / {{ pages }}</span><button class="icon-button" aria-label="下一页订阅用户" :disabled="page >= pages || loading || busy" @click="page++"><AppIcon name="right" /></button></div>
    <div class="dialog-actions"><button :disabled="busy" @click="emit('close')">关闭</button></div>
  </Modal>
</template>
