<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi, type Settings, type SystemInfo } from '../api/system'
import { useAccountScope } from '../community'
import { route } from '../router'
const props = defineProps<{ system: SystemInfo | null; paused?: boolean }>()
const api = useApi()
const paused = computed(() => !!props.paused || route.value.section !== 'settings' || props.system?.database.connected === false)
const scope = useAccountScope(() => paused.value)
type SettingsForm = Settings & { subscription_lease_days: number; history_retention_days: number; public_url: string }
function formValue(value: Settings): SettingsForm {
  return { ...value, subscription_lease_days: value.subscription_lease_days ?? 14, history_retention_days: value.history_retention_days ?? 30, public_url: value.public_url ?? '' }
}
const draft = ref<SettingsForm | null>(null)
const original = ref('')
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const saved = ref('')
const dirty = computed(() => draft.value !== null && JSON.stringify(draft.value) !== original.value)
let loadSequence = 0
async function load() {
  if (loading.value || paused.value) return
  const ticket = scope.ticket(), sequence = ++loadSequence
  loading.value = true; error.value = ''
  try {
    const value = await api<Settings>('/settings')
    if (!scope.current(ticket) || sequence !== loadSequence) return
    draft.value = formValue(value); original.value = JSON.stringify(draft.value)
  } catch (cause) { if (scope.current(ticket) && sequence === loadSequence && !isCancelled(cause)) error.value = errorText(cause) }
  finally { if (sequence === loadSequence) loading.value = false }
}
async function save() {
  const value = draft.value
  if (!value || saving.value || !dirty.value || paused.value) return
  const ticket = scope.ticket()
  error.value = ''; saved.value = ''
  if (![value.timeout_ms, value.probe_concurrency, value.source_concurrency].every(number => Number.isInteger(number) && number > 0)) { error.value = '超时和并发数需要填写大于 0 的整数。'; return }
  try { if (!['https:', 'http:'].includes(new URL(value.test_url).protocol)) throw new Error() }
  catch { error.value = '测试地址需要是有效的 HTTP / HTTPS URL。'; return }
  if (!Number.isInteger(value.subscription_lease_days) || value.subscription_lease_days < 1 || value.subscription_lease_days > 365) { error.value = '订阅租期需要是 1–365 天的整数。'; return }
  if (!Number.isInteger(value.history_retention_days) || value.history_retention_days < 1 || value.history_retention_days > 3650) { error.value = '历史保留需要是 1–3650 天的整数。'; return }
  const publicURL = value.public_url.trim()
  if (publicURL) {
    try { const url = new URL(publicURL); if (!['http:', 'https:'].includes(url.protocol) || url.pathname !== '/' || url.search || url.hash || url.username || url.password) throw new Error() }
    catch { error.value = '公网地址须为不带路径、查询或账号信息的 HTTP(S) 源地址，例如 https://pool.example.com。'; return }
  }
  saving.value = true
  try {
    const result = await api<Settings>('/settings', json('PUT', {
      exclude_pattern: value.exclude_pattern, test_url: value.test_url.trim(), timeout_ms: value.timeout_ms,
      probe_concurrency: value.probe_concurrency, source_concurrency: value.source_concurrency,
      subscription_lease_days: value.subscription_lease_days, history_retention_days: value.history_retention_days, public_url: publicURL,
    }))
    if (!scope.current(ticket)) return
    draft.value = formValue(result); original.value = JSON.stringify(draft.value)
    saved.value = '设置已保存。全局排除规则变化时，来源会按缓存内容重新整理。'
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { saving.value = false }
}
watch(paused, value => {
  if (value) { ++loadSequence; loading.value = false }
  else if (!draft.value) void load()
}, { immediate: true })
</script>
<template>
  <section>
    <div class="page-heading"><div><p class="eyebrow">PREFERENCES</p><h1>运行设置</h1><p class="muted">调整节点筛选和检测策略，让后台按你的节奏运行。</p></div><span v-if="dirty" class="badge queued">有未保存的修改</span></div>
    <div v-if="loading" class="panel empty-state" role="status"><span class="spinner"></span><h3>正在读取设置</h3></div>
    <p v-if="error" class="notice error" role="alert">{{ error }}<button v-if="!draft" :disabled="loading || paused" @click="load">重试</button></p><p v-if="saved" class="notice success" role="status">{{ saved }}</p>
    <form v-if="draft" class="settings-layout" @submit.prevent="save">
      <div class="settings-main">
        <section class="panel">
          <div class="panel-heading"><div><h2>全局排除规则</h2><p class="small muted">让流量提示、到期说明等非节点内容自动离开列表。</p></div><span class="section-number">01</span></div>
          <fieldset :disabled="saving || paused"><label class="form-field"><span class="field-label">节点名称排除正则</span><textarea v-model="draft.exclude_pattern" rows="4" spellcheck="false" placeholder="例如：剩余|到期|过期|流量|官网|套餐|expire|traffic"></textarea><span class="field-hint">与每个来源的排除规则共同生效。留空关闭全局规则；规则由服务端校验。</span></label></fieldset>
          <div class="inset-note">规则修改后会重新处理已保存的来源内容。处理失败时保留原有节点列表。</div>
        </section>
        <section class="panel">
          <div class="panel-heading"><div><h2>节点检测</h2><p class="small muted">通过节点访问测试地址，测量连接延迟与可用性。</p></div><span class="section-number">02</span></div>
          <fieldset :disabled="saving || paused">
            <label class="form-field"><span class="field-label">测试 URL</span><input v-model="draft.test_url" type="url" required placeholder="https://www.gstatic.com/generate_204" /></label>
            <div class="form-grid"><label class="form-field"><span class="field-label">测试超时 <small class="muted">毫秒</small></span><input v-model.number="draft.timeout_ms" type="number" min="1" step="1" required /></label><label class="form-field"><span class="field-label">节点检测并发</span><input v-model.number="draft.probe_concurrency" type="number" min="1" step="1" required /><span class="field-hint">同时进行检测的节点数量。</span></label></div>
          </fieldset>
        </section>
        <section class="panel">
          <div class="panel-heading"><div><h2>来源处理</h2><p class="small muted">控制订阅获取和来源处理的同时执行数量。</p></div><span class="section-number">03</span></div>
          <fieldset :disabled="saving || paused"><label class="form-field"><span class="field-label">来源处理并发</span><input v-model.number="draft.source_concurrency" type="number" min="1" step="1" required /></label></fieldset>
        </section>
        <section class="panel">
          <div class="panel-heading"><div><h2>订阅租期与历史</h2><p class="small muted">管理有效席位租期和来源订阅事件保留时间。</p></div><span class="section-number">04</span></div>
          <fieldset :disabled="saving || paused"><div class="form-grid">
            <label class="form-field"><span class="field-label">订阅租期 <small class="muted">天</small></span><input v-model.number="draft.subscription_lease_days" type="number" min="1" max="365" step="1" required /><span class="field-hint">默认 14 天，支持 1–365 天。网页签到激活、有效探测可续期；普通下载不会续期。</span></label>
            <label class="form-field"><span class="field-label">历史保留 <small class="muted">天</small></span><input v-model.number="draft.history_retention_days" type="number" min="1" max="3650" step="1" required /><span class="field-hint">默认 30 天，支持 1–3650 天。历史不占用有效席位。</span></label>
          </div></fieldset>
        </section>
        <section class="panel">
          <div class="panel-heading"><div><h2>公网访问地址</h2><p class="small muted">用于订阅中的激活与保活地址。</p></div><span class="section-number">05</span></div>
          <fieldset :disabled="saving || paused"><label class="form-field"><span class="field-label">公网 HTTP(S) 源地址 <small class="muted">可选</small></span><input v-model="draft.public_url" type="url" placeholder="https://pool.example.com" /><span class="field-hint">仅协议、主机和可选端口，不带路径。留空时服务按 HTTP + Host 回退；通过 TLS 反向代理部署时请填写真实 HTTPS 公网地址。</span></label></fieldset>
          <p class="small muted">反向代理需要配置 TLS，并转发 WebSocket /ws/ 路由及 Upgrade / Connection 请求头，以便有效探测续期。</p>
        </section>
        <div class="settings-save"><span class="small muted">{{ dirty ? '修改尚未保存' : '当前设置已与服务同步' }}</span><button class="primary" type="submit" :disabled="saving || paused || !dirty"><span v-if="saving" class="spinner"></span>{{ saving ? '正在保存…' : '保存设置' }}</button></div>
      </div>
      <aside class="settings-aside"><section v-if="props.system" class="panel system-card"><h3>服务信息</h3><dl class="detail-list">
        <div><dt>版本</dt><dd>{{ props.system.version }}</dd></div><div><dt>检测内核</dt><dd>{{ props.system.core.state === 'ready' ? '就绪' : '异常' }} {{ props.system.core.version }}</dd></div>
        <div><dt>来源任务</dt><dd>{{ props.system.tasks.active_sources }} 执行 / {{ props.system.tasks.pending_sources }} 排队</dd></div><div><dt>节点任务</dt><dd>{{ props.system.tasks.active_nodes }} 执行 / {{ props.system.tasks.pending_nodes }} 排队</dd></div>
        <div><dt>构建时间</dt><dd>{{ formatDate(props.system.build_time) }}</dd></div><div><dt>提交</dt><dd class="mono">{{ props.system.commit || '—' }}</dd></div>
      </dl></section></aside>
    </form>
  </section>
</template>
