<script setup lang="ts">
import { toastRef } from '../toast'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi, type CommunitySettings, type LoginSettings, type Settings, type SystemInfo } from '../api/system'
import { identityApi } from '../api/identity'
import { levelNames, useAccountScope } from '../community'
import CustomSelect from './CustomSelect.vue'
import TagInput from './TagInput.vue'
import AppIcon from './AppIcon.vue'
import { titleColorNames, titleColors } from '../titles'
import { navigate, route } from '../router'
const props = defineProps<{ system: SystemInfo | null; paused?: boolean }>()
const emit = defineEmits<{ saved: [] }>()
const api = useApi()
const paused = computed(() => !!props.paused || route.value.section !== 'settings' || props.system?.database.connected === false)
const scope = useAccountScope(() => paused.value)
type SettingsForm = Settings & { subscription_lease_days: number; history_retention_days: number; field_test_days: number; idle_check_days: number; public_url: string; community?: CommunitySettings; login?: LoginSettings; maintenance: { enabled: boolean; message: string; since?: string | null } }
function formValue(value: Settings): SettingsForm {
  // 社区设置含数组，深拷贝避免与原值共享引用
  return { ...value, subscription_lease_days: value.subscription_lease_days ?? 14, history_retention_days: value.history_retention_days ?? 30, field_test_days: value.field_test_days ?? 3, idle_check_days: value.idle_check_days ?? 3, public_url: value.public_url ?? '', maintenance: { enabled: !!value.maintenance?.enabled, message: value.maintenance?.message ?? '', since: value.maintenance?.since ?? null }, community: value.community ? JSON.parse(JSON.stringify(value.community)) as CommunitySettings : undefined, login: value.login ? loginForm(value.login) : undefined }
}
// 密钥只写不读：表单里留空表示沿用已保存的值
function loginForm(v: LoginSettings): LoginSettings {
  const c = JSON.parse(JSON.stringify(v)) as LoginSettings
  c.smtp.password = ''; c.github.client_secret = ''; c.linuxdo.client_secret = ''
  return c
}
const defaultSuffixes = ['deepseek.com', 'edu.mo', 'edu.hk', 'edu.tw', 'foxmail.com', '163.com', '126.com', '139.com', '189.cn', 'edu.cn', 'gmail.com', 'aliyun.com', 'apache.org', 'sohu.com', 'privaterelay.appleid.com', 'xiaomi.com', 'yahoo.com', 'sina.cn', 'sina.com', 'qq.com', 'gov.cn']
const suffixTags = computed({ get: () => draft.value?.login?.email_suffixes ?? [], set: (list: string[]) => { if (draft.value?.login) draft.value.login.email_suffixes = list } })
function normalizeSuffix(raw: string) { return raw.trim().toLowerCase().replace(/^.*@/, '').replace(/^\.+/, '') }
function validSuffix(v: string) { return /^(?=.{1,253}$)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$/.test(v) }
function resetSuffixes() { if (draft.value?.login) draft.value.login.email_suffixes = [...defaultSuffixes] }
const callbackBase = computed(() => (draft.value?.public_url.trim() || location.origin).replace(/\/$/, ''))
const testTo = ref('')
const securityOptions = [{ value: 'ssl', label: 'SSL/TLS（常用 465）' }, { value: 'starttls', label: 'STARTTLS（常用 587）' }, { value: 'none', label: '不加密' }]
const testing = ref(false)
async function smtpTest() {
  if (testing.value || !testTo.value.trim()) return
  if (dirty.value) { error.value = '请先保存设置，再发送测试邮件。'; return }
  testing.value = true; error.value = ''
  try { await identityApi.smtpTest(testTo.value.trim()); saved.value = '测试邮件已发送，请查收' }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { testing.value = false }
}
type NumberKey = { [K in keyof CommunitySettings]: CommunitySettings[K] extends number ? K : never }[keyof CommunitySettings]
interface Field { key: NumberKey; label: string; unit?: string; min: number; max: number; hint?: string }
const xpFields: Field[] = [
  { key: 'share_xp_per_user', label: '每位使用者经验', unit: 'XP/天', min: 0, max: 100, hint: '来源每有 1 位其他用户在用，提供方每天获得的经验。' },
  { key: 'share_xp_source_cap', label: '单来源计入人数', unit: '人', min: 1, max: 1000, hint: '超过的人数不再加经验，避免刷人头。' },
  { key: 'share_xp_daily_cap', label: '共享经验每日上限', unit: 'XP', min: 0, max: 10000 },
  { key: 'share_points_percent', label: '共享积分比例', unit: '%', min: 0, max: 100, hint: '共享经验按比例换算为积分。' },
  { key: 'share_quality_full', label: '全额健康线', unit: '%', min: 1, max: 100, hint: '近 24 小时健康节点达到此比例时全额发放。' },
  { key: 'share_quality_half', label: '半额健康线', unit: '%', min: 0, max: 100, hint: '介于两条线之间减半，低于此线不发放。' },
  { key: 'unique_bonus_percent', label: '独有节点加成', unit: '%', min: 0, max: 500 },
  { key: 'unique_share_threshold', label: '独有节点占比', unit: '%', min: 1, max: 100, hint: '来源中平台独有节点达到此比例才有加成。' },
  { key: 'achievement_xp', label: '每项成就经验', unit: 'XP', min: 0, max: 10000 },
  { key: 'probe_xp', label: '客户端续期经验', unit: 'XP/天', min: 0, max: 100, hint: '有效探测自动续期每天发放一次。' },
]
const shopFields: Field[] = [
  { key: 'makeup_price', label: '补签价格', unit: '积分/天', min: 0, max: 100000 },
  { key: 'makeup_monthly', label: '每月可补签', unit: '天', min: 0, max: 31 },
  { key: 'leave_price', label: '请假价格', unit: '积分', min: 0, max: 100000 },
  { key: 'leave_days', label: '每次请假顺延', unit: '天', min: 1, max: 60 },
  { key: 'leave_monthly', label: '每月可请假', unit: '次', min: 0, max: 10 },
  { key: 'boost_days', label: '扩容有效期', unit: '天', min: 1, max: 365 },
  { key: 'boost_node_price', label: '节点扩容价格', unit: '积分', min: 0, max: 100000 },
  { key: 'boost_node_amount', label: '每次节点扩容', unit: '个', min: 1, max: 10000 },
  { key: 'boost_source_price', label: '来源扩容价格', unit: '积分', min: 0, max: 100000 },
  { key: 'boost_subscription_price', label: '订阅扩容价格', unit: '积分', min: 0, max: 100000 },
  { key: 'boost_max_active', label: '同类扩容叠加', unit: '份', min: 0, max: 20 },
  { key: 'tip_max', label: '单次打赏上限', unit: '积分', min: 1, max: 100000 },
]
const riskFields: Field[] = [
  { key: 'risk_daily_new_nodes', label: '每日新增节点', unit: '个', min: 0, max: 100000, hint: '0 表示与账号节点额度相同。' },
  { key: 'risk_daily_new_sources', label: '每日新增跟随来源', unit: '个', min: 0, max: 1000, hint: '0 表示与账号来源额度相同；窗口内跟随过的不同来源不超过来源额度 × 节点倍数。' },
  { key: 'risk_window_days', label: '统计窗口', unit: '天', min: 1, max: 365 },
  { key: 'risk_exposure_multiplier', label: '窗口内节点倍数', unit: '倍', min: 1, max: 100, hint: '窗口内累计拿到的不同节点不超过额度的这个倍数。' },
  { key: 'risk_pulls_per_hour', label: '每小时拉取次数', unit: '次', min: 1, max: 10000, hint: '单个订阅链接，超出返回 429。' },
  { key: 'risk_distinct_ips', label: '每日拉取 IP 数', unit: '个', min: 1, max: 1000, hint: '超过时标记疑似分享链接，提醒管理员。' },
]
const feedbackFields: Field[] = [
  { key: 'feedback_daily_limit', label: '每人每日反馈', unit: '条', min: 1, max: 1000 },
  { key: 'feedback_visible_days', label: '反馈保留天数', unit: '天', min: 1, max: 90, hint: '默认 30 天，超过天数的反馈不再展示，避免旧评价长期影响来源。' },
  { key: 'feedback_reward_xp', label: '反馈被确认奖励', unit: 'XP', min: 0, max: 100, hint: '提供方标记“已处理”后奖励反馈人，每条只发一次。' },
  { key: 'feedback_reward_points', label: '反馈被确认积分', unit: '积分', min: 0, max: 100 },
  { key: 'feedback_reward_daily', label: '每日奖励次数', unit: '次', min: 0, max: 100, hint: '每人每天最多获得几次反馈奖励，0 表示不奖励。' },
  { key: 'feedback_demote_users', label: '节点降序人数', unit: '人', min: 0, max: 100, hint: '24 小时内这么多人反馈不可用/慢/不稳定的节点排到订阅末尾，0 表示关闭。' },
]
const sections = [
  { id: 'maintenance', label: '维护模式', icon: 'lock' }, { id: 'access', label: '访问与注册', icon: 'users' }, { id: 'login', label: '登录与邮件', icon: 'mail' }, { id: 'filter', label: '节点筛选', icon: 'search' }, { id: 'probe', label: '节点检测', icon: 'probe' }, { id: 'source', label: '来源处理', icon: 'sources' },
  { id: 'lease', label: '租期与历史', icon: 'clock' }, { id: 'public', label: '公网地址', icon: 'globe' }, { id: 'quota', label: '等级与额度', icon: 'chart' },
  { id: 'xp', label: '经验与积分', icon: 'star' }, { id: 'shop', label: '积分兑换', icon: 'coin' }, { id: 'risk', label: '风控', icon: 'shield' },
  { id: 'feedback', label: '用户反馈', icon: 'info' }, { id: 'service', label: '数据与服务', icon: 'lock' },
]
const active = ref('maintenance')
function jump(id: string) { active.value = id; document.getElementById(`settings-${id}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' }) }
let observer: IntersectionObserver | null = null
function observe() {
  observer?.disconnect()
  if (typeof IntersectionObserver === 'undefined') return
  observer = new IntersectionObserver(entries => {
    const hit = entries.filter(entry => entry.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)[0]
    if (hit) active.value = hit.target.id.replace('settings-', '')
  }, { rootMargin: '-80px 0px -60% 0px' })
  for (const s of sections) { const el = document.getElementById(`settings-${s.id}`); if (el) observer.observe(el) }
}
onBeforeUnmount(() => observer?.disconnect())
function addTitle() { draft.value?.community?.titles.push({ name: '', price: 100, color: titleColors[(draft.value?.community?.titles.length ?? 0) % titleColors.length] }) }
function removeTitle(index: number) { draft.value?.community?.titles.splice(index, 1) }
function communityError(c: CommunitySettings): string {
  for (const [index, q] of c.level_quotas.entries()) {
    if (![q.nodes, q.sources, q.subscriptions].every(Number.isInteger) || q.nodes < 1 || q.sources < 1 || q.subscriptions < 1) return `Lv${index} 的额度需要是大于 0 的整数。`
  }
  if (c.milestone_xp.some(v => !Number.isInteger(v) || v < 0)) return '里程碑经验需要是不小于 0 的整数。'
  for (const f of [...xpFields, ...shopFields, ...riskFields, ...feedbackFields]) {
    const v = c[f.key]
    if (!Number.isInteger(v) || v < f.min || v > f.max) return `${f.label}需要是 ${f.min}–${f.max} 的整数。`
  }
  if (c.share_quality_half > c.share_quality_full) return '半额健康线不能高于全额健康线。'
  const names = new Set<string>()
  for (const t of c.titles) {
    const name = t.name.trim()
    if (!name || [...name].length > 12 || names.has(name)) return '称号名称需要 1–12 个字且不能重复。'
    if (!Number.isInteger(t.price) || t.price < 0) return '称号价格需要是不小于 0 的整数。'
    names.add(name); t.name = name
  }
  return ''
}
const draft = ref<SettingsForm | null>(null)
const original = ref('')
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const saved = toastRef()
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
    void nextTick(observe)
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
  if (!Number.isInteger(value.field_test_days) || value.field_test_days < 1 || value.field_test_days > 7) { error.value = '实测结果有效天数需要是 1–7 天的整数。'; return }
  if (!Number.isInteger(value.idle_check_days) || value.idle_check_days < 0 || value.idle_check_days > 30) { error.value = '闲置节点检测间隔需要是 0–30 天的整数。'; return }
  if (value.community) { const message = communityError(value.community); if (message) { error.value = message; return } }
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
      subscription_lease_days: value.subscription_lease_days, history_retention_days: value.history_retention_days, field_test_days: value.field_test_days, idle_check_days: value.idle_check_days, public_url: publicURL,
      maintenance: { enabled: value.maintenance.enabled, message: value.maintenance.message.trim() },
      ...(value.community ? { community: value.community } : {}),
      ...(value.login ? { login: { ...value.login, email_suffixes: value.login.email_suffixes.map(v => v.trim()).filter(Boolean), smtp: { ...value.login.smtp, port: Number(value.login.smtp.port) || 0 } } } : {}),
    }))
    if (!scope.current(ticket)) return
    draft.value = formValue(result); original.value = JSON.stringify(draft.value)
    saved.value = '设置已保存'
    emit('saved')
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
    <div class="page-heading"><div><p class="eyebrow">PREFERENCES</p><h1>运行设置</h1><p class="muted">节点筛选、检测、等级额度、积分与风控规则都在这里统一调整。</p></div><span v-if="dirty" class="badge queued">有未保存的修改</span></div>
    <div v-if="loading" class="panel empty-state" role="status"><span class="spinner"></span><h3>正在读取设置</h3></div>
    <p v-if="error" class="notice error" role="alert">{{ error }}<button v-if="!draft" :disabled="loading || paused" @click="load">重试</button></p>
    <form v-if="draft" class="settings-shell" @submit.prevent="save">
      <nav class="settings-nav" aria-label="设置分区"><button v-for="s in sections" v-show="(s.id !== 'quota' && s.id !== 'xp' && s.id !== 'shop' && s.id !== 'risk' && s.id !== 'feedback' || draft.community) && (s.id !== 'login' || draft.login)" :key="s.id" type="button" :class="{ active: active === s.id }" :aria-current="active === s.id ? 'true' : undefined" @click="jump(s.id)"><AppIcon :name="s.icon" />{{ s.label }}</button></nav>
      <div class="settings-main">
        <section id="settings-maintenance" :class="['panel', 'settings-section', 'maintenance-section', { on: draft.maintenance.enabled }]">
          <div class="panel-heading"><div><h2>维护模式</h2><p class="small muted">维护期间普通用户与用户管理员可以登录、查看页面、签到续期和继续拉取订阅，但不能做任何修改；管理员不受影响。</p></div><label class="switch-field"><span>{{ draft.maintenance.enabled ? '维护中' : '未开启' }}</span><button type="button" :class="['switch', { on: draft.maintenance.enabled }]" role="switch" :aria-checked="draft.maintenance.enabled" aria-label="维护模式" :disabled="saving || paused" @click="draft.maintenance.enabled = !draft.maintenance.enabled"><span></span></button></label></div>
          <fieldset :disabled="saving || paused"><label class="form-field"><span class="field-label">维护公告 <small class="muted">可选 · {{ draft.maintenance.message.length }}/300</small></span><textarea v-model="draft.maintenance.message" rows="2" maxlength="300" placeholder="例如：数据库升级中，预计 22:30 恢复，订阅不受影响。"></textarea><span class="field-hint">公告显示在所有用户的页面顶部；被拦截的修改操作也会提示这段文字。</span></label></fieldset>
          <p v-if="draft.maintenance.enabled && draft.maintenance.since" class="small muted">已于 {{ formatDate(draft.maintenance.since) }} 开启。</p>
        </section>
        <section v-if="draft.community" id="settings-access" class="panel settings-section">
          <div class="panel-heading"><div><h2>访问与注册</h2><p class="small muted">控制未登录访客能看到什么，以及是否允许自助注册。</p></div></div>
          <fieldset :disabled="saving || paused">
            <label class="switch-row"><input v-model="draft.community.guest_browse" type="checkbox" class="switch" /><span><strong>允许未登录浏览</strong><small>访客可以查看节点总览和提供方（服务器地址始终隐藏），右上角显示登录与注册；关闭后打开网站直接进入登录页。</small></span></label>
            <label class="switch-row"><input v-model="draft.community.registration_enabled" type="checkbox" class="switch" /><span><strong>开放自助注册</strong><small>新用户注册后为普通用户 Lv0，按等级获得默认额度；同一 IP 每小时最多注册 5 个账号。维护期间暂停注册。</small></span></label>
          </fieldset>
        </section>
        <section v-if="draft.login" id="settings-login" class="panel settings-section">
          <div class="panel-heading"><div><h2>登录与邮件</h2><p class="small muted">配置 SMTP 后启用邮箱注册、验证码登录和找回密码；第三方登录需在对应平台创建 OAuth 应用。</p></div></div>
          <fieldset :disabled="saving || paused">
            <label class="switch-row"><input v-model="draft.login.require_email" type="checkbox" class="switch" /><span><strong>普通用户必须绑定邮箱</strong><small>邮件服务可用时生效：没有邮箱的旧账号、第三方注册账号登录后需要先补齐并验证邮箱才能修改数据。管理员不受限制。</small></span></label>
            <h3 class="settings-subtitle"><AppIcon name="mail" />SMTP 发信</h3>
            <div class="settings-fields">
              <label class="form-field"><span class="field-label">服务器</span><input v-model.trim="draft.login.smtp.host" placeholder="smtp.qq.com" autocomplete="off" /></label>
              <label class="form-field"><span class="field-label">端口</span><input v-model.number="draft.login.smtp.port" type="number" min="1" max="65535" /></label>
              <div class="form-field"><span class="field-label">加密方式</span><CustomSelect v-model="draft.login.smtp.security" label="SMTP 加密方式" :options="securityOptions" /></div>
              <label class="form-field"><span class="field-label">用户名</span><input v-model.trim="draft.login.smtp.username" autocomplete="off" placeholder="通常是完整邮箱" /></label>
              <label class="form-field"><span class="field-label">密码 / 授权码</span><input v-model="draft.login.smtp.password" type="password" autocomplete="new-password" :placeholder="draft.login.smtp.password_set ? '已设置，留空不修改' : '邮箱授权码'" /></label>
              <label class="form-field"><span class="field-label">发件邮箱</span><input v-model.trim="draft.login.smtp.from" type="email" placeholder="noreply@example.com" /></label>
              <label class="form-field"><span class="field-label">发件人名称</span><input v-model.trim="draft.login.smtp.from_name" placeholder="MCII节点共享" /></label>
              <div class="form-field smtp-test"><span class="field-label">发送测试邮件</span><div class="code-row"><input v-model.trim="testTo" type="email" placeholder="收件邮箱" aria-label="测试收件邮箱" /><button type="button" :disabled="testing || !testTo" @click="smtpTest"><span v-if="testing" class="spinner"></span>发送</button></div></div>
            </div>
            <h3 class="settings-subtitle"><AppIcon name="shield" />允许的邮箱后缀</h3>
            <div class="form-field"><span class="field-label field-label-row">输入后按回车或空格添加，子域名自动匹配（如 edu.cn 也允许 xx.edu.cn）<button type="button" class="text-button" @click="resetSuffixes">恢复默认</button></span><TagInput v-model="suffixTags" label="允许的邮箱后缀" placeholder="例如 qq.com" :normalize="normalizeSuffix" :validate="validSuffix" /><span class="field-hint">共 {{ suffixTags.length }} 个；清空表示不限制。仅限制注册和绑定，不影响已有账号登录。</span></div>
            <h3 class="settings-subtitle"><AppIcon name="key" />第三方登录</h3>
            <div class="oauth-settings">
              <article v-for="p in ([{ id: 'github', name: 'GitHub', icon: 'github', doc: 'GitHub → Settings → Developer settings → OAuth Apps' }, { id: 'linuxdo', name: 'LINUX DO', icon: 'linuxdo', doc: 'connect.linux.do → 我的应用接入' }] as const)" :key="p.id" class="oauth-setting">
                <label class="switch-row"><input v-model="draft.login[p.id].enabled" type="checkbox" class="switch" /><span><strong><AppIcon :name="p.icon" />{{ p.name }}</strong><small>{{ p.doc }}</small></span></label>
                <div class="settings-fields">
                  <label class="form-field"><span class="field-label">Client ID</span><input v-model.trim="draft.login[p.id].client_id" autocomplete="off" /></label>
                  <label class="form-field"><span class="field-label">Client Secret</span><input v-model="draft.login[p.id].client_secret" type="password" autocomplete="new-password" :placeholder="draft.login[p.id].secret_set ? '已设置，留空不修改' : ''" /></label>
                </div>
                <p class="small muted callback-url">回调地址：<code>{{ callbackBase }}/api/v1/auth/oauth/{{ p.id }}/callback</code></p>
              </article>
            </div>
          </fieldset>
        </section>
        <section id="settings-filter" class="panel settings-section">
          <div class="panel-heading"><div><h2>全局排除规则</h2><p class="small muted">让流量提示、到期说明等非节点内容自动离开列表。</p></div></div>
          <fieldset :disabled="saving || paused"><label class="form-field"><span class="field-label">节点名称排除正则</span><textarea v-model="draft.exclude_pattern" rows="3" spellcheck="false" placeholder="例如：剩余|到期|过期|流量|官网|套餐|expire|traffic"></textarea><span class="field-hint">与每个来源的排除规则共同生效。留空关闭全局规则；规则由服务端校验。</span></label></fieldset>
          <div class="inset-note">规则修改后会重新处理已保存的来源内容。处理失败时保留原有节点列表。</div>
        </section>
        <section id="settings-probe" class="panel settings-section">
          <div class="panel-heading"><div><h2>节点检测</h2><p class="small muted">通过节点访问测试地址，测量连接延迟与可用性。</p></div></div>
          <fieldset :disabled="saving || paused">
            <label class="form-field"><span class="field-label">测试 URL</span><input v-model="draft.test_url" type="url" required placeholder="https://www.gstatic.com/generate_204" /></label>
            <div class="form-grid"><label class="form-field"><span class="field-label">测试超时 <small class="muted">毫秒</small></span><input v-model.number="draft.timeout_ms" type="number" min="1" step="1" required /></label><label class="form-field"><span class="field-label">节点检测并发</span><input v-model.number="draft.probe_concurrency" type="number" min="1" step="1" required /><span class="field-hint">同时检测的节点数。每个节点完成后立即领取下一个，不再整批等待；首次检测失败的节点直接标记为不可用。</span></label></div>
            <div class="form-grid"><label class="form-field"><span class="field-label">闲置节点检测间隔 <small class="muted">天</small></span><input v-model.number="draft.idle_check_days" type="number" min="0" max="30" step="1" required /><span class="field-hint">默认 3 天，0 表示不定期检测。闲置指没有激活订阅选中、没有人跟随、来源也没人占席位。新入池的节点仍会测一次；有人开始使用后自动恢复为 8 小时（来源有人占席位为 16 小时），订阅来源照常刷新。</span></label></div>
          </fieldset>
        </section>
        <section id="settings-source" class="panel settings-section">
          <div class="panel-heading"><div><h2>来源处理</h2><p class="small muted">控制订阅获取和来源处理的同时执行数量。</p></div></div>
          <fieldset :disabled="saving || paused"><div class="form-grid"><label class="form-field"><span class="field-label">来源处理并发</span><input v-model.number="draft.source_concurrency" type="number" min="1" step="1" required /></label></div>
            <label v-if="draft.community" class="switch-row"><input v-model="draft.community.usage_poll_enabled" type="checkbox" class="switch" /><span><strong>整点读取套餐用量</strong><small>每小时只读取一次来源的 Subscription-Userinfo 响应头（不下载正文），用于运行统计的小时/天流量图和提供方趋势。</small></span></label></fieldset>
        </section>
        <section id="settings-lease" class="panel settings-section">
          <div class="panel-heading"><div><h2>订阅租期与历史</h2><p class="small muted">管理有效席位租期、来源订阅事件保留时间和国内实测统计窗口。</p></div></div>
          <fieldset :disabled="saving || paused"><div class="form-grid">
            <label class="form-field"><span class="field-label">订阅租期 <small class="muted">天</small></span><input v-model.number="draft.subscription_lease_days" type="number" min="1" max="365" step="1" required /><span class="field-hint">默认 14 天，支持 1–365 天。网页签到、有效探测和请假可续期；普通下载不会续期。</span></label>
            <label class="form-field"><span class="field-label">历史保留 <small class="muted">天</small></span><input v-model.number="draft.history_retention_days" type="number" min="1" max="3650" step="1" required /><span class="field-hint">默认 30 天，支持 1–3650 天。历史不占用有效席位。</span></label>
            <label class="form-field"><span class="field-label">实测结果有效天数 <small class="muted">天</small></span><input v-model.number="draft.field_test_days" type="number" min="1" max="7" step="1" required /><span class="field-hint">默认 3 天。国内用户客户端的实测在这段时间内参与排序和展示，更早的不再计入。</span></label>
          </div></fieldset>
        </section>
        <section id="settings-public" class="panel settings-section">
          <div class="panel-heading"><div><h2>公网访问地址</h2><p class="small muted">用于订阅中的激活与保活地址。</p></div></div>
          <fieldset :disabled="saving || paused"><label class="form-field"><span class="field-label">公网 HTTP(S) 源地址 <small class="muted">可选</small></span><input v-model="draft.public_url" type="url" placeholder="https://pool.example.com" /><span class="field-hint">仅协议、主机和可选端口，不带路径。留空时服务按 HTTP + Host 回退；通过 TLS 反向代理部署时请填写真实 HTTPS 公网地址。</span></label></fieldset>
          <div class="inset-note">反向代理需要配置 TLS，并转发 WebSocket /ws/ 路由及 Upgrade / Connection 请求头，以便有效探测续期。</div>
        </section>
        <template v-if="draft.community">
          <section id="settings-quota" class="panel settings-section">
            <div class="panel-heading"><div><h2>等级与默认额度</h2><p class="small muted">每个账号的实际额度 = 等级默认与管理员单独设置取较高者，再加积分临时扩容。</p></div></div>
            <fieldset :disabled="saving || paused"><div class="table-wrap"><table class="quota-table editable"><thead><tr><th>等级</th><th>节点</th><th>来源</th><th>组合订阅</th></tr></thead><tbody>
              <tr v-for="(q, index) in draft.community.level_quotas" :key="index"><th scope="row">Lv{{ index }} <small class="muted">{{ levelNames[index] }}</small></th><td><input v-model.number="q.nodes" type="number" min="1" max="100000" step="1" :aria-label="`Lv${index} 节点额度`" required /></td><td><input v-model.number="q.sources" type="number" min="1" max="10000" step="1" :aria-label="`Lv${index} 来源额度`" required /></td><td><input v-model.number="q.subscriptions" type="number" min="1" max="1000" step="1" :aria-label="`Lv${index} 订阅额度`" required /></td></tr>
            </tbody></table></div></fieldset>
          </section>
          <section id="settings-xp" class="panel settings-section">
            <div class="panel-heading"><div><h2>经验与积分</h2><p class="small muted">签到奖励固定；共享奖励每天北京时间零点后结算，按来源健康度打折。</p></div></div>
            <fieldset :disabled="saving || paused">
              <div class="settings-fields"><label v-for="f in xpFields" :key="f.key" class="form-field"><span class="field-label">{{ f.label }} <small v-if="f.unit" class="muted">{{ f.unit }}</small></span><input v-model.number="draft.community[f.key]" type="number" :min="f.min" :max="f.max" step="1" required /><span v-if="f.hint" class="field-hint">{{ f.hint }}</span></label></div>
              <h3 class="settings-subtitle">稳定共享里程碑</h3>
              <div class="settings-fields"><label v-for="(days, index) in [30, 90, 180]" :key="days" class="form-field"><span class="field-label">连续健康 {{ days }} 天 <small class="muted">XP</small></span><input v-model.number="draft.community.milestone_xp[index]" type="number" min="0" max="100000" step="1" required /></label></div>
            </fieldset>
          </section>
          <section id="settings-shop" class="panel settings-section">
            <div class="panel-heading"><div><h2>积分兑换</h2><p class="small muted">积分只用于便利功能，不能兑换经验或等级。价格为 0 表示免费。</p></div></div>
            <fieldset :disabled="saving || paused">
              <div class="settings-fields"><label v-for="f in shopFields" :key="f.key" class="form-field"><span class="field-label">{{ f.label }} <small v-if="f.unit" class="muted">{{ f.unit }}</small></span><input v-model.number="draft.community[f.key]" type="number" :min="f.min" :max="f.max" step="1" required /><span v-if="f.hint" class="field-hint">{{ f.hint }}</span></label></div>
              <h3 class="settings-subtitle">可兑换称号 <small class="muted">{{ draft.community.titles.length }} / 30</small></h3>
              <ul class="title-editor"><li v-for="(t, index) in draft.community.titles" :key="index"><input v-model="t.name" maxlength="12" placeholder="称号名称" :aria-label="`称号 ${index + 1} 名称`" required /><div class="title-colors" role="radiogroup" :aria-label="`称号 ${index + 1} 颜色`"><button v-for="c in titleColors" :key="c" type="button" role="radio" :aria-checked="(t.color || 'amber') === c" :class="['color-dot', `title-${c}`]" :title="titleColorNames[c]" :aria-label="titleColorNames[c]" @click="t.color = c"></button></div><span :class="['title-chip', `title-${t.color || 'amber'}`]">{{ t.name || '预览' }}</span><label class="title-price"><input v-model.number="t.price" type="number" min="0" max="1000000" step="1" :aria-label="`称号 ${index + 1} 价格`" required /><span class="muted small">积分</span></label><button type="button" class="icon-button danger" :aria-label="`删除称号 ${t.name || index + 1}`" @click="removeTitle(index)"><AppIcon name="trash" /></button></li></ul>
              <button type="button" class="compact" :disabled="draft.community.titles.length >= 30" @click="addTitle"><AppIcon name="plus" />添加称号</button>
            </fieldset>
          </section>
          <section id="settings-risk" class="panel settings-section">
            <div class="panel-heading"><div><h2>防提取风控</h2><p class="small muted">限制频繁更换节点、批量收集节点和公开分享订阅链接。触发时拒绝保存或限流并提示用户，不会自动封禁；多 IP 拉取只做标记，冻结需管理员在用户管理中手动操作。</p></div><label class="switch-field"><span>{{ draft.community.risk_enabled ? '已启用' : '已关闭' }}</span><button type="button" :class="['switch', { on: draft.community.risk_enabled }]" role="switch" :aria-checked="draft.community.risk_enabled" aria-label="启用风控" @click="draft.community.risk_enabled = !draft.community.risk_enabled"><span></span></button></label></div>
            <fieldset :disabled="saving || paused || !draft.community.risk_enabled"><div class="settings-fields"><label v-for="f in riskFields" :key="f.key" class="form-field"><span class="field-label">{{ f.label }} <small v-if="f.unit" class="muted">{{ f.unit }}</small></span><input v-model.number="draft.community[f.key]" type="number" :min="f.min" :max="f.max" step="1" required /><span v-if="f.hint" class="field-hint">{{ f.hint }}</span></label></div></fieldset>
            <div class="inset-note">非提供方看不到节点服务器地址；来源已持有的节点、自己提供的节点不计入新增。关闭风控后仍保留地址隐藏。</div>
          </section>
          <section id="settings-feedback" class="panel settings-section">
            <div class="panel-heading"><div><h2>用户反馈</h2><p class="small muted">仅正在使用节点或来源的用户可以反馈，同一对象 24 小时内只保留一条。</p></div><label class="switch-field"><span>{{ draft.community.feedback_enabled ? '已启用' : '已关闭' }}</span><button type="button" :class="['switch', { on: draft.community.feedback_enabled }]" role="switch" :aria-checked="draft.community.feedback_enabled" aria-label="启用用户反馈" @click="draft.community.feedback_enabled = !draft.community.feedback_enabled"><span></span></button></label></div>
            <fieldset :disabled="saving || paused || !draft.community.feedback_enabled"><div class="settings-fields"><label v-for="f in feedbackFields" :key="f.key" class="form-field"><span class="field-label">{{ f.label }} <small v-if="f.unit" class="muted">{{ f.unit }}</small></span><input v-model.number="draft.community[f.key]" type="number" :min="f.min" :max="f.max" step="1" required /><span v-if="f.hint" class="field-hint">{{ f.hint }}</span></label></div></fieldset>
          </section>
        </template>
        <section v-if="props.system" id="settings-service" class="panel settings-section system-card">
          <div class="panel-heading"><div><h2>数据安全与服务信息</h2><p class="small muted">版本和构建信息只随程序升级或重新构建后变化；任务状态空闲时每 15 秒、有排队任务时每 3 秒刷新。</p></div><a href="/statistics" class="compact-link" @click.prevent="navigate('statistics')">运行统计<AppIcon name="right" /></a></div>
          <div :class="['security-status', props.system.data_encryption ? 'on' : 'off']"><AppIcon :name="props.system.data_encryption ? 'lock' : 'unlock'" /><div><strong>{{ props.system.data_encryption ? '订阅与节点数据已加密存储' : '未启用数据加密' }}</strong><p class="small muted">{{ props.system.data_encryption ? '来源订阅地址、原始内容、节点配置与服务器地址使用 AES-256-GCM 加密，密钥只保存在环境变量 NODEPOOL_DATA_KEY 中。请另行备份密钥，丢失后数据无法读取。' : '设置环境变量 NODEPOOL_DATA_KEY（32 字节 Base64，可用 openssl rand -base64 32 生成）后重启，已有数据会自动加密。' }}</p></div></div>
          <dl class="detail-list service-list">
            <div><dt>版本</dt><dd>{{ props.system.version }}</dd></div><div><dt>检测内核</dt><dd>{{ props.system.core.state === 'ready' ? '就绪' : '异常' }} {{ props.system.core.version }}</dd></div>
            <template v-if="!props.system.tasks_error"><div><dt>来源任务</dt><dd>{{ props.system.tasks.active_sources }} 执行 / {{ props.system.tasks.pending_sources }} 排队</dd></div><div><dt>节点任务</dt><dd>{{ props.system.tasks.active_nodes }} 执行 / {{ props.system.tasks.pending_nodes }} 排队</dd></div></template><div v-else><dt>任务状态</dt><dd>{{ props.system.tasks_error }}</dd></div>
            <div><dt>可信代理</dt><dd>{{ props.system.trusted_proxies ? `${props.system.trusted_proxies} 个网段` : '未配置（按直连 IP 统计）' }}</dd></div>
            <div><dt>构建时间</dt><dd>{{ formatDate(props.system.build_time) }}</dd></div><div><dt>提交</dt><dd class="mono" :title="props.system.commit">{{ props.system.commit && props.system.commit !== 'unknown' ? props.system.commit.slice(0, 12) + (props.system.commit.endsWith('-dirty') ? ' · 未提交修改' : '') : '未提供构建提交' }}</dd></div>
            <div v-if="props.system.sampled_at"><dt>最近采样</dt><dd>{{ formatDate(props.system.sampled_at) }}</dd></div>
          </dl>
        </section>
        <div class="settings-save"><span class="small muted">{{ dirty ? '修改尚未保存' : '当前设置已与服务同步' }}</span><button class="primary" type="submit" :disabled="saving || paused || !dirty"><span v-if="saving" class="spinner"></span>{{ saving ? '正在保存…' : '保存设置' }}</button></div>
      </div>
    </form>
  </section>
</template>
