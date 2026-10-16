<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { errorText, isCancelled, useApi, type Source } from '../api/system'
import type { Page } from '../api/accounts'
import { draftNodes } from '../api/accounts'
import AppIcon from './AppIcon.vue'
const props = withDefaults(defineProps<{ providerId?: string; following?: boolean; selected?: number[]; mode?: 'filter' | 'select' }>(), { mode: 'select', selected: () => [] })
const emit = defineEmits<{ choose: [source: Source]; clear: [] }>()
const api = useApi(); const q = ref(''); const page = ref(1); const data = ref<Page<Source> | null>(null); const loading = ref(false); const error = ref('')
let controller: AbortController | undefined; let sequence=0; let timer: ReturnType<typeof setTimeout> | undefined
async function load() {
  const current=++sequence; controller?.abort(); controller = new AbortController(); loading.value=true
  const params = new URLSearchParams({ q:q.value.trim(), page:String(page.value), page_size:'20' })
  if (props.providerId) params.set('provider_id', props.providerId); if(props.following) params.set('following','1')
  try { const result = await api<Page<Source>>(`/sources/catalog?${params}`, { signal: controller.signal }); if(current === sequence) { data.value=result; error.value='' } }
  catch(cause) { if(!isCancelled(cause) && current===sequence) error.value=errorText(cause) }
  finally { if(current===sequence) loading.value=false }
}
watch(q, () => {page.value=1})
watch([q,page,()=>props.providerId,()=>props.following],()=>{ ++sequence; controller?.abort(); clearTimeout(timer); timer=setTimeout(()=>void load(),250) },{immediate:true})
onBeforeUnmount(()=>{++sequence; clearTimeout(timer); controller?.abort()})
function selectedCount(id: number) { return draftNodes.value.filter(node => node.source_id === id).length }
</script>
<template>
  <div class="source-browser">
    <label class="search-field"><span class="sr-only">搜索共享来源</span><AppIcon name="search" /><input v-model="q" type="search" placeholder="搜索来源名称" /></label>
    <button v-if="mode==='filter'" type="button" class="source-choice all-sources" @click="emit('clear')"><AppIcon name="sources" />全部来源</button>
    <p v-if="error" role="alert" class="notice error">{{ error }}<button @click="load">重试</button></p>
    <div :aria-busy="loading" class="source-browser-items"><button v-for="source in data?.items ?? []" :key="source.id" type="button" class="source-choice" :aria-pressed="mode==='select' ? selected.includes(source.id) : undefined" @click="emit('choose',source)"><AppIcon :name="selected.includes(source.id) ? 'check' : 'sources'" /><span><strong>{{ source.name }}</strong><small>{{ source.provider_name || '提供方' }} · {{ source.node_count }} 节点</small><small v-if="mode === 'select' && (selected.includes(source.id) || selectedCount(source.id))" class="green-text">{{ selected.includes(source.id) ? '已选整个来源' : `已单选 ${selectedCount(source.id)} 个节点` }}</small></span><span class="source-people"><small>当前 {{ source.active_users ?? 0 }} 人</small><small title="历史唯一领取人数；删除订阅不回收名额">历史 {{ source.claimed_users ?? 0 }} / {{ source.max_users || '不限' }}</small></span></button></div>
    <p v-if="loading" role="status" class="small muted">正在读取来源…</p><p v-else-if="!data?.items.length && !error" class="small muted">暂无匹配来源</p>
    <div class="compact-pagination"><span class="small muted">共 {{ data?.total ?? 0 }} 个来源</span><button type="button" class="icon-button" aria-label="上一页来源" :disabled="page===1 || loading" @click="page--"><AppIcon name="left" /></button><span>{{ page }}</span><button type="button" class="icon-button" aria-label="下一页来源" :disabled="loading || page*20 >= (data?.total ?? 0)" @click="page++"><AppIcon name="right" /></button></div>
  </div>
</template>
