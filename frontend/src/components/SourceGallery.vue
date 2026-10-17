<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, isCancelled, useApi, type Source } from '../api/system'
import { draftNodes, draftSources, isAdmin, isGuest, requireLogin, selectingNodes, toggleSource, type Page } from '../api/accounts'
import { blockHint, blockLabel, useAccountScope } from '../community'
import { usePolling } from '../polling'
import AppIcon from './AppIcon.vue'
import CustomSelect from './CustomSelect.vue'
import PageNumbers from './PageNumbers.vue'
import UsageHint from './UsageHint.vue'
import UserAvatar from './UserAvatar.vue'
import FeedbackBadge from './FeedbackBadge.vue'
import { cached, remember } from '../view-cache'
// 按来源浏览：先挑来源再看节点，新上架来源优先曝光
const props = withDefaults(defineProps<{ paused: boolean; q?: string; providerId?: string; following?: boolean; compact?: boolean }>(), { q: '', providerId: '', following: false, compact: false })
const emit = defineEmits<{ view: [source: Source] }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const flags = import.meta.glob<string>('../../node_modules/flag-icons/flags/4x3/[a-z][a-z].svg', { eager: true, query: '?url&no-inline', import: 'default' })
const names = typeof Intl.DisplayNames === 'function' ? new Intl.DisplayNames(['zh-CN'], { type: 'region' }) : null
const data = ref<Page<Source> | null>(null)
const loading = ref(false)
const error = ref('')
const page = ref(1)
const search = ref(props.q)
const sort = ref<'recommend' | 'new'>('recommend')
const onlyAvailable = ref(false)
const pageSize = computed(() => props.compact ? 6 : 12)
const sortOptions = [{ value: 'recommend', label: '推荐' }, { value: 'new', label: '最新上架' }]
let controller: AbortController | undefined
let sequence = 0
let timer: ReturnType<typeof setTimeout> | undefined
function requestPath() {
  const params = new URLSearchParams({ page: String(page.value), page_size: String(pageSize.value), sort: props.compact ? 'new' : sort.value })
  if (!props.compact && search.value.trim()) params.set('q', search.value.trim())
  if (props.providerId) params.set('provider_id', props.providerId)
  if (props.following) params.set('following', '1')
  if (onlyAvailable.value) params.set('available', '1')
  return `/sources/catalog?${params}`
}
async function load() {
  if (props.paused) return
  const ticket = scope.ticket(), current = ++sequence
  controller?.abort(); controller = new AbortController(); loading.value = true
  const path = requestPath()
  try {
    const result = await api<Page<Source>>(path, { signal: controller.signal })
    if (current === sequence && scope.current(ticket)) { data.value = result; error.value = ''; remember(path, result) }
  } catch (cause) { if (!isCancelled(cause) && current === sequence && scope.current(ticket)) error.value = errorText(cause) }
  finally { if (current === sequence) loading.value = false }
}
watch([search, () => props.providerId, () => props.following, sort, onlyAvailable], () => { page.value = 1 })
watch([search, () => props.providerId, () => props.following, sort, onlyAvailable, page, () => props.paused], () => { ++sequence; controller?.abort(); clearTimeout(timer); if (!props.paused) timer = setTimeout(() => void load(), 250) }, { immediate: true })
usePolling(async () => { if (!loading.value && !props.paused) await load() }, 30000)
onBeforeUnmount(() => { ++sequence; clearTimeout(timer); controller?.abort() })
// 小卡片只展示一周内新上架的来源
const items = computed(() => (data.value?.items ?? []).filter(s => !props.compact || s.fresh))
const maxPages = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / pageSize.value)))
function flag(code: string) { return flags[`../../node_modules/flag-icons/flags/4x3/${code.toLowerCase()}.svg`] }
function country(code: string) { try { return names?.of(code) || code } catch { return code } }
function health(s: Source) { return s.node_count ? Math.round((s.healthy_count ?? 0) / s.node_count * 100) : 0 }
function seats(s: Source) { return s.max_users ? `${s.occupied_users ?? s.active_users ?? 0} / ${s.max_users}` : '不限' }
function picked(s: Source) { return draftSources.value.some(v => v.id === s.id) }
function partial(s: Source) { return draftNodes.value.filter(n => n.source_id === s.id).length }
function add(s: Source) {
  if (requireLogin('登录后即可跟随整个来源。')) return
  if (s.unavailable && !picked(s)) return
  toggleSource(s)
  if (!selectingNodes.value && picked(s)) selectingNodes.value = true
}
data.value ??= cached<Page<Source>>(requestPath()) ?? null
</script>

<template>
  <section v-if="!compact || items.length" :class="['source-gallery', { compact }]" :aria-busy="loading">
    <div v-if="compact" class="gallery-head"><div><h2><AppIcon name="star" />新上架来源</h2><p class="small muted">最近 7 天加入的来源会优先展示，帮助新来源获得曝光。</p></div></div>
    <div v-else class="gallery-toolbar">
      <label class="search-field"><span class="sr-only">搜索来源</span><AppIcon name="search" /><input v-model="search" type="search" placeholder="搜索来源名称" /></label>
      <CustomSelect v-model="sort" label="来源排序" :options="sortOptions" />
      <label v-if="!isAdmin" class="switch-chip"><input v-model="onlyAvailable" type="checkbox" class="switch" />只看可加入</label>
      <span class="small muted">共 {{ data?.total ?? 0 }} 个来源</span>
    </div>
    <p v-if="error" class="notice error" role="alert">{{ error }}<button @click="load">重试</button></p>
    <div v-if="items.length" class="gallery-grid">
      <article v-for="s in items" :key="s.id" :class="['gallery-card', { unavailable: !!s.unavailable, picked: picked(s) }]">
        <header class="gallery-card-head"><UserAvatar :id="s.owner_id" :name="s.provider_name || s.provider_username || '提供方'" :version="s.provider_avatar" :size="34" /><div><strong :title="s.name">{{ s.name }}</strong><small class="muted">{{ s.provider_name || s.provider_username || '提供方' }}</small></div></header>
        <div class="gallery-tags"><span v-if="s.fresh" class="mini-tag tone-violet">新上架</span><span v-if="s.unavailable" class="mini-tag tone-red" :title="blockHint(s.unavailable, s.min_level)">{{ blockLabel(s.unavailable, s.min_level) }}</span><span v-else-if="s.seated" class="mini-tag tone-green">已在使用</span><span v-if="!s.max_users" class="mini-tag tone-blue" title="未设置使用人数上限，选用它不占你的来源额度">不占来源额度</span><span v-if="s.min_level" class="mini-tag">Lv{{ s.min_level }}+</span><FeedbackBadge :summary="s.feedback" /></div>
        <dl class="gallery-stats"><div><dt>健康</dt><dd :class="{ 'green-text': health(s) >= 80 }">{{ s.healthy_count ?? 0 }}<small> / {{ s.node_count }}</small></dd></div><div><dt>平均延迟</dt><dd>{{ s.avg_latency ? `${s.avg_latency} ms` : '—' }}</dd></div><div><dt>席位</dt><dd :class="{ 'danger-text': s.unavailable === 'full' }">{{ seats(s) }}</dd></div></dl>
        <div v-if="s.countries?.length" class="gallery-flags"><span v-for="c in s.countries" :key="c" :title="country(c)"><img v-if="flag(c)" :src="flag(c)" alt="" width="18" height="13" /><b v-else>{{ c }}</b></span></div>
        <UsageHint v-if="!compact" :usage="s.subscription_usage" bar />
        <footer class="gallery-actions"><button type="button" class="compact" @click="emit('view', s)">查看节点<AppIcon name="right" /></button><button v-if="!isGuest || !compact" type="button" :class="['compact', { primary: !picked(s) && !s.unavailable }]" :disabled="!!s.unavailable && !picked(s)" :title="s.unavailable ? blockHint(s.unavailable, s.min_level) : ''" @click="add(s)"><AppIcon :name="picked(s) ? 'check' : 'plus'" />{{ picked(s) ? '已跟随' : partial(s) ? `已单选 ${partial(s)}` : '跟随来源' }}</button></footer>
      </article>
    </div>
    <div v-else-if="loading && !data" class="empty-state" role="status"><span class="spinner"></span><h3>正在读取来源</h3></div>
    <div v-else-if="!compact && !error" class="empty-state"><span class="empty-icon"><AppIcon name="sources" /></span><h3>没有符合条件的来源</h3><p>{{ onlyAvailable ? '可以关闭“只看可加入”查看全部来源。' : '尝试调整搜索词或范围。' }}</p></div>
    <div v-if="!compact && (data?.total ?? 0) > pageSize" class="pagination"><span class="small muted">第 {{ page }} / {{ maxPages }} 页</span><PageNumbers v-model="page" :pages="maxPages" :disabled="loading" previous-label="上一页来源" next-label="下一页来源" /></div>
  </section>
</template>
