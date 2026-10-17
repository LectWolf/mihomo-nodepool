<script setup lang="ts">
import { toastRef } from '../toast'
import { onBeforeUnmount, ref, watch } from 'vue'
import { errorText, isCancelled, useApi } from '../api/system'
import { absoluteURL, copyText, followed, isGuest, requireLogin, toggleFollow, type Page, type Provider } from '../api/accounts'
import { navigate, route, routeParams, setRouteQuery } from '../router'
import AppIcon from './AppIcon.vue'
import TitleChip from './TitleChip.vue'
import PageNumbers from './PageNumbers.vue'
import UserAvatar from './UserAvatar.vue'
import Modal from './Modal.vue'
import { walletApi } from '../api/economy'
import { currentUser } from '../api/accounts'
import { isStaff } from '../api/moderation'
import SourceModeration from './SourceModeration.vue'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const q = ref(''); const onlyFollowing = ref(false); const page = ref(1)
const data = ref<Page<Provider> | null>(null); const loading = ref(false)
const error = ref(''); const message = toastRef(); const busy = ref(0)
let timer: ReturnType<typeof setTimeout> | undefined
let sequence = 0; let controller: AbortController | undefined; let restoring = false
function restore() {
  if (route.value.section !== 'providers') return
  restoring = true
  const p = routeParams('providers'); q.value = p.get('q') ?? ''; onlyFollowing.value = p.get('following') === '1'
  const n = Number(p.get('page')); page.value = Number.isInteger(n) && n > 0 ? n : 1
  restoring = false
}
watch(route, restore, { immediate: true, flush: 'sync' })
watch([q, onlyFollowing], () => { if (!restoring) page.value = 1 }, { flush: 'sync' })
watch([q, onlyFollowing, page], ([query, only, p], [oldQuery]) => {
  if (restoring || route.value.section !== 'providers') return
  const params = new URLSearchParams()
  if (query.trim()) params.set('q', query.trim())
  if (only) params.set('following', '1')
  if (p > 1) params.set('page', String(p))
  setRouteQuery('providers', params, query !== oldQuery)
})
async function load() {
  if (props.paused) return
  const current = ++sequence; controller?.abort(); controller = new AbortController(); loading.value = true
  const params = new URLSearchParams({ q: q.value, page: String(page.value), page_size: '20' })
  if (onlyFollowing.value) params.set('following', '1')
  try {
    const result = await api<Page<Provider>>(`/providers?${params}`, { signal: controller.signal })
    if (current !== sequence) return
    const last = Math.max(1, Math.ceil(result.total / 20))
    if (page.value > last) { page.value = last; return }
    data.value = result; error.value = ''
    const ids = new Set(followed.value)
    result.items.forEach(p => { if (p.followed) ids.add(p.id); else ids.delete(p.id) })
    followed.value = ids
  } catch (cause) { if (!isCancelled(cause) && current === sequence) error.value = errorText(cause) }
  finally { if (current === sequence) loading.value = false }
}
watch([q, onlyFollowing, page, () => props.paused], () => {
  clearTimeout(timer); controller?.abort(); ++sequence
  if (!props.paused) timer = setTimeout(() => void load(), 250)
  else loading.value = false
}, { immediate: true })
onBeforeUnmount(() => { clearTimeout(timer); controller?.abort(); ++sequence })
async function follow(p: Provider) {
  if (requireLogin('登录后即可关注提供方，并在节点总览里只看关注的节点。')) return
  if (busy.value) return
  busy.value = p.id
  try { await toggleFollow(p.id); await load() }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = 0 }
}
const tipping = ref<Provider | null>(null)
const tipAmount = ref(10)
const tipBusy = ref(false)
const tipError = ref('')
function openTip(p: Provider) {
  if (requireLogin('登录后即可用积分打赏提供方。')) return
  tipping.value = p; tipAmount.value = 10; tipError.value = ''
}
async function sendTip() {
  const p = tipping.value
  if (!p || tipBusy.value) return
  tipBusy.value = true; tipError.value = ''
  try { await walletApi.tip(p.id, tipAmount.value); message.value = `已向「${p.display_name || p.username}」打赏 ${tipAmount.value} 积分。`; tipping.value = null }
  catch (cause) { if (!isCancelled(cause)) tipError.value = errorText(cause) }
  finally { tipBusy.value = false }
}
// 来源管理：null 关闭，0 表示全部提供方
const moderating = ref<Provider | 0 | null>(null)
function viewProvider(id: number) { navigate('nodes', new URLSearchParams({ provider_id: String(id) })) }
async function share(p: Provider) {
  try { await copyText(absoluteURL(p.provider_path || `/nodes?provider_id=${p.id}`)); message.value = '提供方入口已复制，登录后可选择其节点。' }
  catch (cause) { error.value = errorText(cause) }
}
</script>
<template>
  <section class="providers-page">
    <div class="page-heading"><div><p class="eyebrow">PROVIDERS</p><h1>提供方</h1><p class="muted">发现节点来源，关注你信任的提供方。{{ data ? `共 ${data.total} 位提供方。` : '' }}</p></div><button v-if="isStaff" type="button" @click="moderating = 0"><AppIcon name="shield" />来源管理</button></div>
    <div class="directory-toolbar"><div class="scope-tabs" role="group" aria-label="提供方范围"><button :aria-pressed="!onlyFollowing" @click="onlyFollowing = false">全部提供方</button><button v-if="!isGuest" :aria-pressed="onlyFollowing" @click="onlyFollowing = true"><AppIcon name="heart" />已关注</button></div><label class="search-field"><span class="sr-only">搜索提供方</span><AppIcon name="search" /><input v-model="q" type="search" placeholder="搜索提供方" /></label></div>
    <p v-if="error" class="notice error" role="alert">{{ error }}<button @click="load">重试</button></p>
    <div class="provider-grid" :aria-busy="loading">
      <article v-for="provider in data?.items ?? []" :key="provider.id" class="panel provider-card">
        <div class="provider-card-head"><UserAvatar :id="provider.id" :name="provider.display_name || provider.username" :version="provider.avatar_version" :size="44" /><div><h2>{{ provider.display_name || provider.username }}</h2><span class="small muted">@{{ provider.username }}<TitleChip :name="provider.title" /></span></div><button class="icon-button follow-button" :aria-label="followed.has(provider.id) ? `取消关注 ${provider.display_name}` : `关注 ${provider.display_name}`" :aria-pressed="followed.has(provider.id)" :disabled="!!busy" @click="follow(provider)"><AppIcon name="heart" /></button></div>
        <div class="provider-counts"><span><strong>{{ provider.source_count }}</strong>来源</span><span><strong>{{ provider.node_count }}</strong>节点</span><span><strong>{{ provider.followers }}</strong>关注者</span></div>
        <div class="provider-card-actions"><button @click="viewProvider(provider.id)">查看节点<AppIcon name="right" /></button><button v-if="provider.id !== currentUser?.id && provider.source_count" class="icon-button" :aria-label="`打赏 ${provider.display_name || provider.username}`" title="打赏积分" @click="openTip(provider)"><AppIcon name="gift" /></button><button class="icon-button" :aria-label="`复制 ${provider.display_name} 的入口`" title="复制提供方入口" @click="share(provider)"><AppIcon name="copy" /></button><button v-if="isStaff" class="icon-button" :aria-label="`管理 ${provider.display_name || provider.username} 的来源`" title="管理与下架来源" @click="moderating = provider"><AppIcon name="shield" /></button></div>
      </article>
    </div>
    <div v-if="loading && !data" class="empty-state" role="status"><span class="spinner"></span><h3>正在读取提供方</h3></div>
    <div v-else-if="!loading && !data?.items.length && !error" class="empty-state"><span class="empty-icon"><AppIcon name="users" /></span><h3>{{ onlyFollowing ? '还没有关注的提供方' : '暂无匹配提供方' }}</h3><p>{{ onlyFollowing ? '在提供方卡片上点心形即可关注，关注后可在节点总览里只看他们的节点。' : '尝试换个关键词。' }}</p></div>
    <Modal v-if="tipping" title="打赏积分" :busy="tipBusy" @close="tipping = null"><form @submit.prevent="sendTip">
      <div class="tip-head"><UserAvatar :id="tipping.id" :name="tipping.display_name || tipping.username" :version="tipping.avatar_version" :size="40" /><div><strong>{{ tipping.display_name || tipping.username }}</strong><p class="small muted">积分直接转给提供方，不影响任何人的经验和等级。</p></div></div>
      <div class="tip-presets"><button v-for="value in [10, 30, 50, 100]" :key="value" type="button" class="chip-button" :aria-pressed="tipAmount === value" @click="tipAmount = value">{{ value }}</button></div>
      <label class="form-field"><span class="field-label">打赏积分</span><input v-model.number="tipAmount" type="number" min="1" step="1" required /><span class="field-hint">当前可用 {{ currentUser?.points ?? 0 }} 积分。</span></label>
      <p v-if="tipError" class="notice error" role="alert">{{ tipError }}</p>
      <div class="dialog-actions"><button type="button" :disabled="tipBusy" @click="tipping = null">取消</button><button class="primary" :disabled="tipBusy || !(tipAmount > 0)">{{ tipBusy ? '正在打赏…' : `打赏 ${tipAmount || 0} 积分` }}</button></div>
    </form></Modal>
    <SourceModeration v-if="moderating !== null" :owner-id="moderating ? moderating.id : undefined" :owner-name="moderating ? moderating.display_name || moderating.username : undefined" @close="moderating = null" @changed="load" />
    <div class="pagination"><span class="small muted">{{ loading ? '正在读取…' : `共 ${data?.total ?? 0} 个提供方` }}</span><PageNumbers v-model="page" :pages="Math.max(1, Math.ceil((data?.total ?? 0) / 20))" :disabled="loading" previous-label="上一页提供方" next-label="下一页提供方" /></div>
  </section>
</template>
