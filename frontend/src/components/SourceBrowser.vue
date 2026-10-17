<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { errorText, isCancelled, useApi, type Source } from '../api/system'
import type { Page } from '../api/accounts'
import { currentUser, draftNodes } from '../api/accounts'
import { blockLabel, communityUser, useAccountScope } from '../community'
import UsageHint from './UsageHint.vue'
import '../community.css'
import AppIcon from './AppIcon.vue'
import PageNumbers from './PageNumbers.vue'
const props = withDefaults(defineProps<{ providerId?: string; following?: boolean; exclusive?: boolean; selected?: number[]; mode?: 'filter' | 'select' }>(), { mode: 'select', selected: () => [] })
const emit = defineEmits<{ choose: [source: Source]; clear: [] }>()
const api = useApi(); const q = ref(''); const page = ref(1); const data = ref<Page<Source> | null>(null); const loading = ref(false); const error = ref('')
const scope = useAccountScope()
let controller: AbortController | undefined; let sequence=0; let timer: ReturnType<typeof setTimeout> | undefined
async function load() {
  const ticket = scope.ticket()
  const current=++sequence; controller?.abort(); controller = new AbortController(); loading.value=true
  const params = new URLSearchParams({ q:q.value.trim(), page:String(page.value), page_size:'20', sort:'recommend' })
  if (props.providerId) params.set('provider_id', props.providerId); if(props.following) params.set('following','1'); if(props.exclusive) params.set('exclusive','1')
  try { const result = await api<Page<Source>>(`/sources/catalog?${params}`, { signal: controller.signal }); if(current === sequence && scope.current(ticket)) { data.value=result; error.value='' } }
  catch(cause) { if(!isCancelled(cause) && current===sequence && scope.current(ticket)) error.value=errorText(cause) }
  finally { if(current===sequence) loading.value=false }
}
watch(q, () => {page.value=1})
watch([q,page,()=>props.providerId,()=>props.following,()=>props.exclusive],()=>{ ++sequence; controller?.abort(); clearTimeout(timer); timer=setTimeout(()=>void load(),250) },{immediate:true})
onBeforeUnmount(()=>{++sequence; clearTimeout(timer); controller?.abort()})
function selectedCount(id: number) { return draftNodes.value.filter(node => node.source_id === id).length }
function belowLevel(source: Source) { return currentUser.value?.role !== 'admin' && source.owner_id !== currentUser.value?.id && communityUser.value.level < (source.min_level ?? 0) }
function full(source: Source) { return !!source.max_users && (source.occupied_users ?? source.active_users ?? 0) >= source.max_users }
function reason(source: Source) { return !source.enabled ? '来源已停用' : belowLevel(source) ? `需要 Lv${source.min_level}，当前 Lv${communityUser.value.level}` : source.unavailable ? `${blockLabel(source.unavailable, source.min_level)}，暂时无法加入` : '' }
function choose(source: Source) { if (props.mode === 'select' && reason(source) && !props.selected.includes(source.id)) return; emit('choose', source) }
</script>
<template>
  <div class="source-browser">
    <label class="search-field"><span class="sr-only">搜索共享来源</span><AppIcon name="search" /><input v-model="q" type="search" placeholder="搜索来源名称" /></label>
    <button v-if="mode==='filter'" type="button" class="source-choice all-sources" @click="emit('clear')"><AppIcon name="sources" />全部来源</button>
    <p v-if="error" role="alert" class="notice error">{{ error }}<button @click="load">重试</button></p>
    <div :aria-busy="loading" class="source-browser-items">
      <button v-for="source in data?.items ?? []" :key="source.id" type="button" class="source-choice" :aria-pressed="mode === 'select' ? selected.includes(source.id) : undefined" :disabled="mode === 'select' && !!reason(source) && !selected.includes(source.id)" :title="reason(source) || source.name" @click="choose(source)">
        <AppIcon :name="selected.includes(source.id) ? 'check' : 'sources'" />
        <span><strong :title="source.name">{{ source.name }}</strong><small>{{ source.provider_name || '提供方' }} · {{ source.node_count }} 节点 · {{ source.min_level ? `最低 Lv${source.min_level}` : 'Lv0 不限' }}{{ source.max_users ? '' : ' · 不占来源额度' }}</small><small v-if="reason(source)" class="danger-text">{{ reason(source) }}</small><small v-if="mode === 'select' && (selected.includes(source.id) || selectedCount(source.id))" class="green-text">{{ selected.includes(source.id) ? '已选整个来源' : `已单选 ${selectedCount(source.id)} 个节点` }}</small></span>
        <span class="source-people"><small :class="{ 'danger-text': full(source) }">席位 {{ source.occupied_users ?? source.active_users ?? 0 }} / {{ source.max_users || '不限' }}{{ full(source) ? ' · 满额' : '' }}</small></span>
        <UsageHint :usage="source.subscription_usage" bar />
      </button>
    </div>
    <p v-if="loading" role="status" class="small muted">正在读取来源…</p><p v-else-if="!data?.items.length && !error" class="small muted">暂无匹配来源</p>
    <div class="compact-pagination"><span class="small muted">共 {{ data?.total ?? 0 }} 个来源</span><PageNumbers v-model="page" :pages="Math.max(1, Math.ceil((data?.total ?? 0) / 20))" :disabled="loading" previous-label="上一页来源" next-label="下一页来源" /></div>
  </div>
</template>
