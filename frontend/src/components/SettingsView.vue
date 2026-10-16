<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi, type Settings, type SystemInfo } from '../api/system'
const props = defineProps<{ system: SystemInfo | null }>()
const api = useApi()
const draft = ref<Settings | null>(null)
const original = ref('')
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const saved = ref('')
const dirty = computed(() => draft.value !== null && JSON.stringify(draft.value) !== original.value)
async function load() {
  if (loading.value) return
  loading.value = true; error.value = ''
  try {
    const value = await api<Settings>('/settings')
    draft.value = { ...value }; original.value = JSON.stringify(value)
  } catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { loading.value = false }
}
async function save() {
  const value = draft.value
  if (!value || saving.value || !dirty.value) return
  error.value = ''; saved.value = ''
  if (![value.timeout_ms, value.probe_concurrency, value.source_concurrency].every(number => Number.isInteger(number) && number > 0)) { error.value = '超时和并发数需要填写大于 0 的整数。'; return }
  try { if (!['https:', 'http:'].includes(new URL(value.test_url).protocol)) throw new Error() }
  catch { error.value = '测试地址需要是有效的 HTTP / HTTPS URL。'; return }
  saving.value = true
  try {
    const result = await api<Settings>('/settings', json('PUT', { exclude_pattern: value.exclude_pattern, test_url: value.test_url.trim(), timeout_ms: value.timeout_ms, probe_concurrency: value.probe_concurrency, source_concurrency: value.source_concurrency }))
    draft.value = { ...result }; original.value = JSON.stringify(result)
    saved.value = '设置已保存。全局排除规则变化时，来源会按缓存内容重新整理。'
  } catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { saving.value = false }
}
onMounted(() => void load())
</script>

<template>
  <section>
    <div class="page-heading"><div><p class="eyebrow">PREFERENCES</p><h1>运行设置</h1><p class="muted">调整节点筛选和检测策略，让后台按你的节奏运行。</p></div><span v-if="dirty" class="badge queued">有未保存的修改</span></div>
    <div v-if="loading" class="panel empty-state" role="status"><span class="spinner"></span><h3>正在读取设置</h3></div>
    <p v-if="error" class="notice error" role="alert">{{ error }}<button v-if="!draft" :disabled="loading" @click="load">重试</button></p><p v-if="saved" class="notice success" role="status">{{ saved }}</p>
    <form v-if="draft" class="settings-layout" @submit.prevent="save">
      <div class="settings-main"><section class="panel"><div class="panel-heading"><div><h2>全局排除规则</h2><p class="small muted">让流量提示、到期说明等非节点内容自动离开列表。</p></div><span class="section-number">01</span></div><fieldset :disabled="saving"><label class="form-field"><span class="field-label">节点名称排除正则</span><textarea v-model="draft.exclude_pattern" rows="4" spellcheck="false" placeholder="例如：剩余|到期|过期|流量|官网|套餐|expire|traffic"></textarea><span class="field-hint">与每个来源的排除规则共同生效。留空关闭全局规则；规则由服务端校验。</span></label></fieldset><div class="inset-note">规则修改后会重新处理已保存的来源内容。处理失败时保留原有节点列表。</div></section>
      <section class="panel"><div class="panel-heading"><div><h2>节点检测</h2><p class="small muted">通过节点访问测试地址，测量连接延迟与可用性。</p></div><span class="section-number">02</span></div><fieldset :disabled="saving"><label class="form-field"><span class="field-label">测试 URL</span><input v-model="draft.test_url" type="url" required placeholder="https://www.gstatic.com/generate_204" /></label><div class="form-grid"><label class="form-field"><span class="field-label">测试超时 <small class="muted">毫秒</small></span><input v-model.number="draft.timeout_ms" type="number" min="1" step="1" required /></label><label class="form-field"><span class="field-label">节点检测并发</span><input v-model.number="draft.probe_concurrency" type="number" min="1" step="1" required /><span class="field-hint">同时进行检测的节点数量。</span></label></div></fieldset></section>
      <section class="panel"><div class="panel-heading"><div><h2>来源处理</h2><p class="small muted">控制订阅获取和来源处理的同时执行数量。</p></div><span class="section-number">03</span></div><fieldset :disabled="saving"><label class="form-field"><span class="field-label">来源处理并发</span><input v-model.number="draft.source_concurrency" type="number" min="1" step="1" required /></label></fieldset></section>
      <div class="settings-save"><span class="small muted">{{ dirty ? '修改尚未保存' : '当前设置已与服务同步' }}</span><button class="primary" type="submit" :disabled="saving || !dirty"><span v-if="saving" class="spinner"></span>{{ saving ? '正在保存…' : '保存设置' }}</button></div></div>
      <aside class="settings-aside"><section v-if="props.system" class="panel system-card"><h3>服务信息</h3><dl class="detail-list"><div><dt>版本</dt><dd>{{ props.system.version }}</dd></div><div><dt>检测内核</dt><dd>{{ props.system.core.state === 'ready' ? '就绪' : '异常' }} {{ props.system.core.version }}</dd></div><div><dt>来源任务</dt><dd>{{ props.system.tasks.active_sources }} 执行 / {{ props.system.tasks.pending_sources }} 排队</dd></div><div><dt>节点任务</dt><dd>{{ props.system.tasks.active_nodes }} 执行 / {{ props.system.tasks.pending_nodes }} 排队</dd></div><div><dt>构建时间</dt><dd>{{ formatDate(props.system.build_time) }}</dd></div><div><dt>提交</dt><dd class="mono">{{ props.system.commit || '—' }}</dd></div></dl></section></aside>
    </form>
  </section>
</template>
