<script setup lang="ts">
import { toastRef } from '../toast'
import TitleChip from './TitleChip.vue'
import { computed, ref, watch } from 'vue'
import { currentUser } from '../api/accounts'
import { errorText, formatDate, isCancelled, json, useApi, type Source } from '../api/system'
import { levelLabel, useAccountScope, type RetainedPage, type Subscriber } from '../community'
import Modal from './Modal.vue'
import UserAvatar from './UserAvatar.vue'
import PageNumbers from './PageNumbers.vue'
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
const message = toastRef()
// 待确认的踢出：ban 为 true 时同时禁止再次领取
const kicking = ref<{ user: Subscriber; ban: boolean } | null>(null)
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
// 与后端 renew_method 取值对应：claim 为领取时的初始值
function renewal(method: string) { return ({ claim: '首次领取', checkin: '网页签到', probe: '客户端探测', leave: '请假延期', makeup: '补签' } as Record<string, string>)[method] || '其他方式' }
const done = { remove: '已从该用户全部组合中移除此来源并释放席位，对方之后仍可重新加入。', kick: '已从该用户全部组合中移除此来源，并禁止再次领取。', unblock: '已解除此来源的领取禁用，用户需要重新选择并激活。' }
async function moderate(user: Subscriber, action: 'remove' | 'kick' | 'unblock') {
  if (busy.value || !canManage(user) || (action !== 'unblock' && kicking.value?.user.user_id !== user.user_id)) return
  const ticket = scope.ticket()
  busy.value = true; error.value = ''; message.value = ''
  try {
    await api(`/sources/${props.source.id}/subscribers/${user.user_id}`, json('PATCH', { action }))
    if (!scope.current(ticket)) return
    kicking.value = null; message.value = done[action]
    emit('changed'); await load()
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
</script>
<template>
  <Modal title="订阅用户" wide :busy="busy" @close="emit('close')">
    <h3 class="ellipsis" :title="source.name">{{ source.name }}</h3>
    <p class="small muted">有效席位 {{ source.occupied_users ?? source.active_users ?? 0 }} / {{ source.max_users || '不限' }}，历史记录保留 {{ data?.history_retention_days ?? 30 }} 天。</p>
    <div class="tab-bar" role="tablist" aria-label="订阅用户范围"><button v-for="tab in tabs" :key="tab.value" role="tab" :aria-selected="state === tab.value" :class="{ active: state === tab.value }" :disabled="busy" @click="state = tab.value">{{ tab.label }}</button></div>

    <p v-if="error" class="notice error small" role="alert">{{ error }}<button :disabled="loading || busy" @click="load">重试</button></p>
    <p v-if="loading" class="small muted" role="status">正在读取{{ state === 'current' ? '当前订阅' : '订阅记录' }}…</p>
    <div :aria-busy="loading">
      <article v-for="user in data?.items ?? []" :key="user.user_id" class="subscriber-card">
        <div class="subscriber-heading"><UserAvatar :id="user.user_id" :name="user.display_name || user.username" :version="user.avatar_version" :size="36" /><div><strong :title="user.display_name || user.username">{{ user.display_name || user.username }}</strong><span class="small muted">{{ user.username }} · {{ levelLabel(user.level) }} · {{ user.xp }} XP<TitleChip :name="user.title" /><span v-if="!user.enabled"> · 账号停用</span></span></div><span class="badge" :class="user.state === 'active' ? 'ready' : user.state === 'kicked' ? 'error' : 'queued'">{{ states[user.state] }}</span></div>
        <p class="small muted">本来源引用：{{ user.node_count }} 个节点 · {{ user.subscription_count }} 个组合</p>
        <dl class="subscriber-times">
          <div><dt>首次领取</dt><dd>{{ formatDate(user.claimed_at) }}</dd></div><div><dt>租期到期</dt><dd>{{ formatDate(user.expires_at) }}</dd></div>
          <div><dt>最近拉取</dt><dd>{{ user.last_pull_at ? formatDate(user.last_pull_at) : '尚无记录' }}</dd></div><div><dt>最近续期</dt><dd>{{ user.last_renewed_at ? `${formatDate(user.last_renewed_at)} · ${renewal(user.renew_method)}` : '尚未续期' }}</dd></div>
          <div v-if="user.ended_at"><dt>结束时间</dt><dd>{{ formatDate(user.ended_at) }}</dd></div>
        </dl>
        <div v-if="canManage(user)" class="source-community-actions"><button v-if="user.state === 'kicked'" class="compact" :disabled="busy" @click="moderate(user, 'unblock')">解除领取禁用</button><template v-else><button class="compact" :disabled="busy" title="移出组合并释放席位，对方之后可重新加入" @click="kicking = { user, ban: false }; error = ''">踢出</button><button class="compact danger" :disabled="busy" @click="kicking = { user, ban: true }; error = ''">踢出并禁止领取</button></template></div>
        <p v-else-if="user.user_id === source.owner_id" class="small muted">来源所有者不可踢出。</p>
      </article>
      <p v-if="!loading && !error && !data?.items.length" class="small muted">{{ state === 'current' ? '暂无当前在订用户。' : '暂无历史记录。' }}</p>
    </div>
    <div v-if="kicking" class="subscriber-confirm" role="alertdialog" aria-label="确认踢出订阅用户"><p v-if="kicking.ban">确定踢出「{{ kicking.user.display_name || kicking.user.username }}」并禁止再次领取吗？此来源会从其所有组合中移除，现有有效席位将释放。</p><p v-else>确定踢出「{{ kicking.user.display_name || kicking.user.username }}」吗？此来源会从其所有组合中移除并释放席位，对方之后仍可重新加入。</p><div class="dialog-actions"><button :disabled="busy" @click="kicking = null">取消</button><button class="danger-solid" :disabled="busy" @click="moderate(kicking.user, kicking.ban ? 'kick' : 'remove')">{{ kicking.ban ? '确认踢出并禁用' : '确认踢出' }}</button></div></div>
    <div class="compact-pagination"><span class="small muted">共 {{ data?.total ?? 0 }} 人</span><PageNumbers v-model="page" :pages="pages" :disabled="loading || busy" previous-label="上一页订阅用户" next-label="下一页订阅用户" /></div>
    <div class="dialog-actions"><button :disabled="busy" @click="emit('close')">关闭</button></div>
  </Modal>
</template>
