<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi } from '../api/system'
import type { RuleProfile } from '../api/accounts'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const items = ref<RuleProfile[]>([])
const error = ref('')
const message = ref('')
const loading = ref(false)
const busy = ref(false)
const editorOpen = ref(false)
const editing = ref<number | null>(null)
const deleting = ref<RuleProfile | null>(null)
const formError = ref('')
const form = reactive({ name: '', routing_enabled: true, dns_enabled: true, dns: '', rules: '' })
const ruleCount = computed(() => form.rules.split('\n').filter(line => line.trim() && !line.trim().startsWith('#')).length)
async function load() {
  if (props.paused || loading.value) return
  loading.value = true
  try { items.value = (await api<{ items: RuleProfile[] }>('/rule-profiles')).items; error.value = '' }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { loading.value = false }
}
watch(() => props.paused, paused => { if (!paused) void load(); else { editorOpen.value = false; deleting.value = null } }, { immediate: true })
function fill(item: RuleProfile, id: number | null, name = item.name) {
  editing.value = id; form.name = name; form.routing_enabled = item.routing_enabled; form.dns_enabled = item.dns_enabled
  form.dns = (item.dns_servers ?? []).join('\n'); form.rules = (item.rules ?? []).join('\n'); formError.value = ''; editorOpen.value = true
}
async function create() {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { const template = await api<RuleProfile>('/rule-profiles/default'); if (!props.paused) fill(template, null, '默认规则') }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
async function restore() {
  if (busy.value) return
  busy.value = true; formError.value = ''
  try { const template = await api<RuleProfile>('/rule-profiles/default'); form.rules = template.rules.join('\n') }
  catch (cause) { if (!isCancelled(cause)) formError.value = errorText(cause) }
  finally { busy.value = false }
}
async function save() {
  if (busy.value || !form.name.trim()) return
  busy.value = true; formError.value = ''
  try {
    const payload = { name: form.name.trim(), routing_enabled: form.routing_enabled, dns_enabled: form.dns_enabled, dns_servers: form.dns.split(/[\s,，;；]+/).filter(Boolean), rules: form.rules.split('\n').map(line => line.trim()).filter(line => line && !line.startsWith('#')) }
    await api(editing.value ? `/rule-profiles/${editing.value}` : '/rule-profiles', json(editing.value ? 'PUT' : 'POST', payload))
    editorOpen.value = false; message.value = '规则组合已保存，绑定它的订阅将在客户端下次更新时应用。'; await load()
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
    <div class="page-heading"><div><h1>规则组合</h1><p class="muted">保存多套分流与 DNS，在我的订阅中自由绑定。只影响 Clash / Mihomo。</p></div><button class="primary" :disabled="busy || paused" @click="create"><AppIcon name="plus" />复制默认规则</button></div>
    <p v-if="error" role="alert" class="notice error">{{ error }}<button @click="load">重试</button></p>
    <p v-if="message" role="status" class="notice success">{{ message }}</p>
    <div v-if="loading" class="panel empty-state" role="status"><span class="spinner"></span><p>正在读取规则组合…</p></div>
    <div v-else-if="!items.length && !error" class="panel empty-state"><AppIcon name="settings" /><h3>从默认规则开始</h3><p>包含国内直连、广告拦截、自动选择和故障转移。复制后可以按需修改。</p><button class="primary" :disabled="busy" @click="create">创建规则组合</button></div>
    <div class="rule-grid"><article v-for="item in items" :key="item.id" class="panel rule-card"><div class="panel-heading"><h2 :title="item.name">{{ item.name }}</h2><button class="compact" @click="fill(item, item.id)"><AppIcon name="edit" />编辑</button></div><div class="rule-status"><span :class="{ enabled: item.routing_enabled }">分流 {{ item.routing_enabled ? '开启' : '跟随来源' }}</span><span :class="{ enabled: item.dns_enabled }">DNS {{ item.dns_enabled ? '开启' : '关闭' }}</span></div><p class="small muted">{{ item.rules.length.toLocaleString() }} 条规则 · {{ item.dns_enabled ? (item.dns_servers.length ? `${item.dns_servers.length} 个 DNS` : '默认 DNS') : '不写入 DNS' }}</p><p class="small muted">{{ formatDate(item.updated_at) }}</p><div class="subscription-maintenance"><button class="text-button" @click="fill(item, null, `${item.name} 副本`)"><AppIcon name="copy" />复制</button><button class="text-button danger" @click="deleting = item; formError = ''"><AppIcon name="trash" />删除</button></div></article></div>
    <Modal v-if="editorOpen" :title="editing ? '编辑规则组合' : '创建规则组合'" wide :busy="busy" @close="editorOpen = false"><form @submit.prevent="save">
      <label class="form-field"><span class="field-label">组合名称</span><input v-model="form.name" required maxlength="200" :disabled="busy" /></label>
      <div class="rule-option"><div><strong>分流规则</strong><p class="small muted">使用国内直连、广告拦截等覆盖规则。关闭则跟随来源。只影响 Clash。</p></div><button type="button" class="switch" :class="{ on: form.routing_enabled }" role="switch" aria-label="分流规则" :aria-checked="form.routing_enabled" :disabled="busy" @click="form.routing_enabled = !form.routing_enabled"><span></span></button></div>
      <details class="rule-editor"><summary>查看和修改分流规则 <span class="count">{{ ruleCount.toLocaleString() }} 条</span></summary><label class="form-field"><span class="field-label">Clash 规则 <small class="muted">每行一条，按顺序匹配</small></span><textarea v-model="form.rules" class="mono rule-textarea" spellcheck="false" :disabled="busy" aria-label="Clash 分流规则"></textarea><span class="field-hint">目标可使用 NodePool、DIRECT、REJECT，或默认策略组名称。最后保留 MATCH 兜底。</span></label><button type="button" class="text-button" :disabled="busy" @click="restore">恢复默认分流规则</button></details>
      <div class="rule-option"><div><strong>自定义 DNS</strong><p class="small muted">留空使用 223.5.5.5、119.29.29.29、8.8.8.8。关闭则不写入 DNS。</p></div><button type="button" class="switch" :class="{ on: form.dns_enabled }" role="switch" aria-label="自定义 DNS" :aria-checked="form.dns_enabled" :disabled="busy" @click="form.dns_enabled = !form.dns_enabled"><span></span></button></div>
      <label v-if="form.dns_enabled" class="form-field"><span class="field-label">DNS 服务器 <small class="muted">可选</small></span><textarea v-model="form.dns" class="mono" rows="3" placeholder="223.5.5.5&#10;119.29.29.29&#10;8.8.8.8" :disabled="busy" /><span class="field-hint">每行一个，也可用逗号分隔。</span></label>
      <p v-if="formError" role="alert" class="notice error">{{ formError }}</p><div class="dialog-actions"><button type="button" :disabled="busy" @click="editorOpen = false">取消</button><button class="primary" :disabled="busy || !form.name.trim()">{{ busy ? '正在保存…' : '保存规则组合' }}</button></div>
    </form></Modal>
    <Modal v-if="deleting" title="删除规则组合" :busy="busy" @close="deleting = null"><p>删除「{{ deleting.name }}」？请先在我的订阅中解除绑定。</p><p v-if="formError" role="alert" class="notice error">{{ formError }}</p><div class="dialog-actions"><button :disabled="busy" @click="deleting = null">取消</button><button class="danger-solid" :disabled="busy" @click="remove">确认删除</button></div></Modal>
  </section>
</template>
