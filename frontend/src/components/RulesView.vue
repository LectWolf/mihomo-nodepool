<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi } from '../api/system'
import type { RuleProfile } from '../api/accounts'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import RuleProfileEditor from './RuleProfileEditor.vue'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const items = ref<RuleProfile[]>([])
const official = computed(() => items.value.find(item => item.official || item.id === 0))
const personal = computed(() => items.value.filter(item => !item.official && item.id !== 0))
const error = ref('')
const message = ref('')
const loading = ref(false)
const busy = ref(false)
const editor = ref<RuleProfile | null>(null)
const editorKey = ref(0)
const editing = ref<number | null>(null)
const viewing = ref(false)
const deleting = ref<RuleProfile | null>(null)
const formError = ref('')
async function load() {
  if (props.paused || loading.value) return
  loading.value = true
  try { items.value = (await api<{ items: RuleProfile[] }>('/rule-profiles')).items; error.value = '' }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { loading.value = false }
}
watch(() => props.paused, paused => { if (!paused) void load(); else { editor.value = null; deleting.value = null } }, { immediate: true })
function fill(item: RuleProfile, copy = false) {
  editing.value = copy ? null : item.id; viewing.value = !copy && (!!item.official || item.id === 0)
  editor.value = { ...item, name: copy ? item.id === 0 ? '我的规则组合' : `${item.name} 副本` : item.name, dns_servers: [...item.dns_servers], rules: [...item.rules] }; editorKey.value++; formError.value = ''
}
async function create() {
  if (busy.value) return
  if (official.value) { fill(official.value, true); return }
  busy.value = true; error.value = ''
  try { const template = await api<RuleProfile>('/rule-profiles/default'); if (!props.paused) fill(template, true) }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
async function save(item: RuleProfile) {
  if (busy.value || viewing.value) return
  busy.value = true; formError.value = ''
  try {
    const { name, routing_enabled, dns_enabled, dns_servers, rules, groups } = item
    await api(editing.value ? `/rule-profiles/${editing.value}` : '/rule-profiles', json(editing.value ? 'PUT' : 'POST', { name, routing_enabled, dns_enabled, dns_servers, rules, groups }))
    editor.value = null; message.value = '规则组合已保存，绑定它的订阅将在客户端下次更新时应用。'; await load()
  } catch (cause) { if (!isCancelled(cause)) formError.value = errorText(cause) }
  finally { busy.value = false }
}
async function remove() {
  if (!deleting.value || busy.value) return
  busy.value = true; formError.value = ''
  try { await api(`/rule-profiles/${deleting.value.id}`, json('DELETE')); deleting.value = null; message.value = '规则组合已删除。'; await load() }
  catch (cause) { if (!isCancelled(cause)) formError.value = errorText(cause) }
  finally { busy.value = false }
}
</script>
<template>
  <section>
    <div class="page-heading"><div><p class="eyebrow">ROUTING & DNS</p><h1>规则组合</h1><p class="muted">官方规则开箱即用，私人组合按需调整。</p></div><button class="primary" :disabled="busy || paused" @click="create"><AppIcon name="plus" />创建规则组合</button></div>
    <p v-if="error" role="alert" class="notice error">{{ error }}<button @click="load">重试</button></p>
    <p v-if="message" role="status" class="notice success">{{ message }}</p>
    <div v-if="loading && !items.length" class="panel empty-state" role="status"><span class="spinner"></span><p>正在读取规则组合…</p></div>
    <article v-if="official" class="panel official-rule-card"><div class="official-rule-symbol"><AppIcon name="globe" /></div><div class="official-rule-content"><span class="official-label"><AppIcon name="check" />官方提供 · 默认推荐</span><h2>官方默认规则</h2><p>国内直连、广告拦截、自动选择与故障转移。</p><div class="official-rule-meta"><span>{{ official.rules.length.toLocaleString() }} 条分流规则</span><span>默认 DNS 已开启</span><span>所有账号可用</span></div></div><div class="official-rule-actions"><button :disabled="busy" @click="fill(official)">查看规则</button><button :disabled="busy" @click="fill(official, true)"><AppIcon name="copy" />复制并编辑</button></div></article>
    <div class="personal-rule-heading"><div><h2>我的规则组合 <span class="count">{{ personal.length }}</span></h2><p class="small muted">可供多个订阅复用，修改后统一生效。</p></div></div>
    <div v-if="!loading && !personal.length && !error" class="panel empty-personal-rules"><AppIcon name="settings" /><div><h3>需要不同策略？创建自己的组合</h3><p class="small muted">复制官方规则，再添加优先规则或自定义 DNS。官方规则始终保留。</p></div><button :disabled="busy" @click="create">复制官方规则</button></div>
    <div class="rule-grid"><article v-for="item in personal" :key="item.id" class="panel rule-card"><div class="panel-heading"><div><span class="eyebrow">私人组合</span><h2 :title="item.name">{{ item.name }}</h2></div><button class="compact" @click="fill(item)"><AppIcon name="edit" />编辑</button></div><div class="rule-status"><span :class="{ enabled: item.routing_enabled }"><AppIcon name="sources" />{{ item.routing_enabled ? '分流已开启' : '跟随来源' }}</span><span :class="{ enabled: item.dns_enabled }"><AppIcon name="globe" />{{ item.dns_enabled ? 'DNS 已开启' : 'DNS 已关闭' }}</span></div><p class="small muted">{{ item.rules.length.toLocaleString() }} 条规则 · {{ item.dns_enabled ? item.dns_servers.length ? `${item.dns_servers.length} 个 DNS` : '默认 DNS' : '不写入 DNS' }}</p><div class="rule-card-footer"><span class="small muted">更新于 {{ formatDate(item.updated_at) }}</span><button class="text-button" @click="fill(item, true)"><AppIcon name="copy" />复制</button><button class="text-button danger" @click="deleting = item; formError = ''"><AppIcon name="trash" />删除</button></div></article></div>
    <Modal v-if="editor" :title="viewing ? '官方规则详情' : editing ? '编辑规则组合' : '创建规则组合'" expanded :busy="busy" @close="editor = null"><RuleProfileEditor :key="editorKey" :profile="editor" :readonly="viewing" :busy="busy" :error="formError" @save="save" @cancel="editor = null" @copy="fill(editor!, true)" /></Modal>
    <Modal v-if="deleting" title="删除规则组合" :busy="busy" @close="deleting = null"><p>删除「{{ deleting.name }}」？请先在我的订阅中解除绑定。</p><p v-if="formError" role="alert" class="notice error">{{ formError }}</p><div class="dialog-actions"><button :disabled="busy" @click="deleting = null">取消</button><button class="danger-solid" :disabled="busy" @click="remove">确认删除</button></div></Modal>
  </section>
</template>
