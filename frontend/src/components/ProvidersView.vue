<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { errorText, isCancelled, useApi } from '../api/system'
import { absoluteURL, copyText, followed, toggleFollow, type Page, type Provider } from '../api/accounts'
import { navigate, route, routeParams, setRouteQuery } from '../router'
import AppIcon from './AppIcon.vue'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const q = ref(''); const onlyFollowing = ref(false); const page = ref(1)
const data = ref<Page<Provider> | null>(null); const loading = ref(false)
const error = ref(''); const message = ref(''); const busy = ref(0)
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
  if (busy.value) return
  busy.value = p.id
  try { await toggleFollow(p.id); await load() }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = 0 }
}
function viewProvider(id: number) { navigate('nodes', new URLSearchParams({ provider_id: String(id) })) }
async function share(p: Provider) {
  try { await copyText(absoluteURL(p.provider_path || `/nodes?provider_id=${p.id}`)); message.value = '提供方入口已复制，登录后可选择其节点。' }
  catch (cause) { error.value = errorText(cause) }
}
</script>
<template>
  <section>
    <div class="page-heading"><div><h1>提供方</h1><p class="muted">发现节点来源，关注你信任的提供方。</p></div></div>
    <div class="directory-toolbar"><div class="scope-tabs" role="group" aria-label="提供方范围"><button :aria-pressed="!onlyFollowing" @click="onlyFollowing = false">全部提供方</button><button :aria-pressed="onlyFollowing" @click="onlyFollowing = true"><AppIcon name="heart" />已关注</button></div><label class="search-field"><span class="sr-only">搜索提供方</span><AppIcon name="search" /><input v-model="q" type="search" placeholder="搜索提供方" /></label></div>
    <p v-if="message" class="notice success" role="status">{{ message }}</p><p v-if="error" class="notice error" role="alert">{{ error }}<button @click="load">重试</button></p>
    <div class="provider-grid" :aria-busy="loading">
      <article v-for="provider in data?.items ?? []" :key="provider.id" class="panel provider-card">
        <div class="provider-card-head"><span class="provider-avatar">{{ (provider.display_name || provider.username).slice(0, 1).toUpperCase() }}</span><div><h2>{{ provider.display_name || provider.username }}</h2><span class="small muted">@{{ provider.username }}</span></div><button class="icon-button" :aria-label="followed.has(provider.id) ? `取消关注 ${provider.display_name}` : `关注 ${provider.display_name}`" :aria-pressed="followed.has(provider.id)" :disabled="!!busy" @click="follow(provider)"><AppIcon name="heart" /></button></div>
        <div class="provider-counts"><span><strong>{{ provider.source_count }}</strong>来源</span><span><strong>{{ provider.node_count }}</strong>节点</span><span><strong>{{ provider.followers }}</strong>关注者</span></div>
        <div class="provider-card-actions"><button @click="viewProvider(provider.id)">查看节点<AppIcon name="right" /></button><button class="icon-button" :aria-label="`复制 ${provider.display_name} 的入口`" title="复制提供方入口" @click="share(provider)"><AppIcon name="copy" /></button></div>
      </article>
    </div>
    <div v-if="!loading && !data?.items.length && !error" class="empty-state"><AppIcon name="users" /><h3>{{ onlyFollowing ? '还没有关注的提供方' : '暂无匹配提供方' }}</h3></div>
    <div class="pagination"><span class="small muted">{{ loading ? '正在读取…' : `共 ${data?.total ?? 0} 个提供方` }}</span><div><button aria-label="上一页提供方" :disabled="loading || page === 1" @click="page--"><AppIcon name="left" /></button><span>{{ page }}</span><button aria-label="下一页提供方" :disabled="loading || page * 20 >= (data?.total ?? 0)" @click="page++"><AppIcon name="right" /></button></div></div>
  </section>
</template>
