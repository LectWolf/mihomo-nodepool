<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi } from '../api/system'
import { currentUser, isAdmin, loadProfile, roleNames, type Page, type User } from '../api/accounts'
import { levelForXP, levelLabel, levelProgress, userCommunity, useAccountScope } from '../community'
import { toast } from '../toast'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import TitleChip from './TitleChip.vue'
import PageNumbers from './PageNumbers.vue'
import UserAvatar from './UserAvatar.vue'
import CustomSelect from './CustomSelect.vue'
import UserDetailDialog from './UserDetailDialog.vue'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
interface Risk { window_nodes: number; window_limit: number; window_days: number; today_new: number; daily_new_cap: number; today_ips: number; today_pulls: number; distinct_ips_threshold: number; flags: string[] | null; frozen_until?: string; last_exposure?: string; window_sources?: number; source_window_limit?: number; today_new_sources?: number; source_daily_cap?: number }
interface Quota { nodes: number; sources: number; subscriptions: number }
type UserPage = Page<User> & { limits?: Record<number, Quota & { level?: Quota; personal?: Quota; boost?: Quota }>; risk?: Record<number, Risk> }
const data = ref<UserPage | null>(null)
const riskUser = ref<User | null>(null)
const viewing = ref<User | null>(null)
function editFromDetail(user: User) { viewing.value = null; edit(user) }
const riskBusy = ref(false)
const riskError = ref('')
const flagNames: Record<string, string> = { frequent_swaps: '频繁更换节点', frequent_source_swaps: '频繁更换来源', daily_new_high: '今日新增过多', many_ips: '多 IP 拉取', frozen: '已冻结' }
function effective(user: User) { return data.value?.limits?.[user.id] ?? { nodes: user.node_limit, sources: user.source_limit, subscriptions: user.subscription_limit } }
function levelQuota(user: User | null): Quota | undefined { return user ? data.value?.limits?.[user.id]?.level : undefined }
function personal(user: User) { return user.node_limit > 0 || user.source_limit > 0 || user.subscription_limit > 0 }
function boosted(user: User) { const b = data.value?.limits?.[user.id]?.boost; return !!b && (b.nodes > 0 || b.sources > 0 || b.subscriptions > 0) }
function risk(user: User) { return data.value?.risk?.[user.id] }
function riskTone(user: User) { const flags = risk(user)?.flags ?? []; return flags.includes('frozen') ? 'frozen' : flags.length ? 'warn' : 'ok' }
function riskText(user: User) { const flags = risk(user)?.flags ?? []; return flags.length ? flagNames[flags[0]] + (flags.length > 1 ? ` +${flags.length - 1}` : '') : '正常' }
function riskTitle(user: User) { const r = risk(user); return r ? `${r.window_days} 天内 ${r.window_nodes}/${r.window_limit} 个节点，今日新增 ${r.today_new}/${r.daily_new_cap}，跟随来源 ${r.window_sources ?? 0}/${r.source_window_limit ?? 0}，今日 ${r.today_ips} 个 IP 拉取 ${r.today_pulls} 次` : '暂无数据' }
// 用户管理员只能处理普通用户
function manageable(user: User) { return isAdmin.value || user.role === 'user' }
async function riskAction(body: { freeze_hours?: number; reset_exposure?: boolean }, done: string) {
  const user = riskUser.value
  if (!user || riskBusy.value) return
  riskBusy.value = true; riskError.value = ''
  try { await api(`/users/${user.id}/risk`, json('POST', body)); toast(done); riskUser.value = null; await load() }
  catch (cause) { if (!isCancelled(cause)) riskError.value = errorText(cause) }
  finally { riskBusy.value = false }
}
const q = ref('')
// 筛选条件：角色、状态、风控、额度方式
const filter = reactive({ role: '', status: '', risk: '', quota: '' })
const roleFilter = [{ value: '', label: '全部角色' }, { value: 'admin', label: '管理员' }, { value: 'manager', label: '用户管理员' }, { value: 'user', label: '普通用户' }]
const statusFilter = [{ value: '', label: '全部状态' }, { value: 'enabled', label: '已启用' }, { value: 'disabled', label: '已停用' }]
const quotaFilter = [{ value: '', label: '全部额度' }, { value: 'custom', label: '单独设置额度' }, { value: 'level', label: '跟随等级' }]
const riskChips = [{ value: '', label: '全部' }, { value: 'flagged', label: '触发风控' }, { value: 'frozen', label: '已冻结' }, { value: 'clean', label: '正常' }]
const filtering = computed(() => !!(q.value || filter.role || filter.status || filter.risk || filter.quota))
function clearFilters() { q.value = ''; Object.assign(filter, { role: '', status: '', risk: '', quota: '' }) }
const page = ref(1)
const loading = ref(false)
const error = ref('')
const open = ref(false)
const editing = ref<User | null>(null)
const saving = ref(false)
const formError = ref('')
const form = reactive({ username: '', display_name: '', password: '', enabled: true, role: 'user' as User['role'], custom: false, node_limit: 0, source_limit: 0, subscription_limit: 0, xp: 0, points: 0, email: '' })
let timer: ReturnType<typeof setTimeout> | undefined
let sequence = 0
let controller: AbortController | undefined
async function load() {
  if (props.paused) return
  const current = ++sequence, ticket = scope.ticket()
  controller?.abort(); controller = new AbortController(); loading.value = true
  try {
    const params = new URLSearchParams({ q: q.value, page: String(page.value), page_size: '20' })
    for (const [key, value] of Object.entries(filter)) if (value) params.set(key, value)
    const result = await api<UserPage>(`/users?${params}`, { signal: controller.signal })
    if (current === sequence && scope.current(ticket)) { data.value = result; error.value = '' }
  } catch (cause) { if (current === sequence && scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { if (current === sequence) loading.value = false }
}
watch([q, filter], () => { page.value = 1 })
watch([q, page, filter, () => props.paused], () => {
  clearTimeout(timer); controller?.abort(); ++sequence
  if (!props.paused) timer = setTimeout(() => void load(), 250)
  else { open.value = false; form.password = ''; loading.value = false }
}, { immediate: true })
onBeforeUnmount(() => { clearTimeout(timer); controller?.abort(); ++sequence })
function edit(user: User | null) {
  const community = userCommunity(user), level = levelQuota(user)
  editing.value = user
  const custom = !!user && personal(user)
  Object.assign(form, {
    username: user?.username ?? '', display_name: user?.display_name ?? '', password: '', enabled: user?.enabled ?? true, role: user?.role ?? 'user', custom,
    node_limit: custom ? user!.node_limit || level?.nodes || 0 : level?.nodes ?? 0, source_limit: custom ? user!.source_limit || level?.sources || 0 : level?.sources ?? 0,
    subscription_limit: custom ? user!.subscription_limit || level?.subscriptions || 0 : level?.subscriptions ?? 0, xp: community.xp, points: community.points, email: user?.email ?? '',
  })
  formError.value = ''; open.value = true
}
// 打开“单独设置额度”时以等级默认值为起点，关闭则发送 0 表示跟随等级
watch(() => form.custom, on => {
  const level = levelQuota(editing.value)
  if (on && level && !form.node_limit) Object.assign(form, { node_limit: level.nodes, source_limit: level.sources, subscription_limit: level.subscriptions })
})
const limitsValid = computed(() => !form.custom || [form.node_limit, form.source_limit, form.subscription_limit].every(n => Number.isInteger(n) && n > 0))
const emailOk = computed(() => !form.email.trim() || /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.email.trim()))
const valid = computed(() => emailOk.value && !!form.username.trim() && (!!editing.value || form.password.length >= 8) && limitsValid.value && [form.xp, form.points].every(n => Number.isSafeInteger(n) && n >= 0))
const resultingLevel = computed(() => levelLabel(levelForXP(form.xp)))
const roleLocked = computed(() => !isAdmin.value || editing.value?.id === 1 || editing.value?.id === currentUser.value?.id)
const roleOptions = [
  { value: 'user', label: '普通用户', hint: '使用节点、共享来源' },
  { value: 'manager', label: '用户管理员', hint: '管理普通用户，无系统设置' },
  { value: 'admin', label: '管理员', hint: '全部权限' },
] as const
async function save() {
  if (!valid.value || saving.value || props.paused) return
  const ticket = scope.ticket(), target = editing.value
  saving.value = true; formError.value = ''
  try {
    const limits = form.custom ? { node_limit: form.node_limit, source_limit: form.source_limit, subscription_limit: form.subscription_limit } : { node_limit: 0, source_limit: 0, subscription_limit: 0 }
    const body = { display_name: form.display_name.trim(), ...limits, ...(form.password ? { password: form.password } : {}), ...(!roleLocked.value || !target ? { role: form.role } : {}) }
    if (target) await api(`/users/${target.id}`, json('PATCH', { ...body, enabled: form.enabled, xp: form.xp, points: form.points, ...(form.email.trim() !== (target.email ?? '') ? { email: form.email.trim() } : {}) }))
    else await api('/users', json('POST', { ...body, username: form.username.trim() }))
    if (!scope.current(ticket)) return
    open.value = false; form.password = ''; toast(target ? '用户设置已保存' : '用户已创建，可使用用户名和密码登录')
    await load()
    if (target?.id === currentUser.value?.id && scope.current(ticket)) await loadProfile()
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) formError.value = errorText(cause) }
  finally { saving.value = false }
}
</script>
<template>
  <section class="users-page">
    <div class="page-heading"><div><p class="eyebrow">ACCOUNTS</p><h1>用户管理</h1><p class="muted">管理账号、角色、等级积分与额度。{{ isAdmin ? '' : '你是用户管理员，只能管理普通用户。' }}</p></div><button class="primary" :disabled="paused" @click="edit(null)"><AppIcon name="plus" />创建用户</button></div>
    <p v-if="error" class="notice error" role="alert">{{ error }}<button @click="load">重试</button></p>
    <div class="panel users-panel">
      <div class="panel-heading"><h2>{{ filtering ? '筛选结果' : '全部用户' }} <span class="count">{{ data?.total ?? 0 }}</span></h2><label class="search-field"><span class="sr-only">搜索用户</span><AppIcon name="search" /><input v-model="q" type="search" placeholder="搜索用户名、昵称或邮箱" /></label></div>
      <div class="user-filters">
        <div class="scope-tabs" role="group" aria-label="风控筛选"><button v-for="chip in riskChips" :key="chip.value" type="button" :aria-pressed="filter.risk === chip.value" @click="filter.risk = chip.value"><AppIcon v-if="chip.value === 'flagged'" name="alert" /><AppIcon v-else-if="chip.value === 'frozen'" name="lock" />{{ chip.label }}</button></div>
        <CustomSelect v-model="filter.role" label="角色筛选" :options="roleFilter" />
        <CustomSelect v-model="filter.status" label="状态筛选" :options="statusFilter" />
        <CustomSelect v-model="filter.quota" label="额度筛选" :options="quotaFilter" />
        <button v-if="filtering" type="button" class="text-button" @click="clearFilters"><AppIcon name="reset" />清除筛选</button>
      </div>
      <ul class="user-list" :aria-busy="loading">
        <li v-for="user in data?.items ?? []" :key="user.id" :class="{ off: !user.enabled }">
          <div class="user-main"><UserAvatar :id="user.id" :name="user.display_name || user.username" :version="user.avatar_version" :size="42" /><button type="button" class="user-names user-names-button" :title="`查看 ${user.display_name || user.username} 的详情`" @click="viewing = user"><strong>{{ user.display_name || user.username }}<TitleChip :name="user.title" /></strong><span class="cell-sub">@{{ user.username }} · <span class="user-email-line"><template v-if="user.email"><AppIcon name="mail" />{{ user.email }}</template><template v-else>未绑定邮箱</template></span></span></button></div>
          <div class="user-tags"><span :class="['role-badge', user.role]"><AppIcon :name="user.role === 'user' ? 'users' : 'shield'" />{{ roleNames[user.role] ?? user.role }}</span><span class="badge" :class="user.enabled ? 'ready' : 'error'">{{ user.enabled ? '启用' : '停用' }}</span></div>
          <div class="user-level"><span class="user-k">等级</span><strong>{{ levelLabel(userCommunity(user).level) }}</strong><div class="xp-track" :title="`${userCommunity(user).xp} XP`"><i :style="{ width: `${levelProgress(userCommunity(user).xp).percent}%` }"></i></div><span class="cell-sub">{{ userCommunity(user).xp.toLocaleString() }} XP · {{ userCommunity(user).points.toLocaleString() }} 积分</span></div>
          <div class="user-quota"><span class="user-k">额度 <small>节点 / 来源 / 订阅</small></span><strong class="mono">{{ effective(user).nodes }} / {{ effective(user).sources }} / {{ effective(user).subscriptions }}</strong><span class="cell-sub"><span v-if="personal(user)" class="mini-tag amber">单独设置</span><span v-else class="mini-tag">跟随等级</span><span v-if="boosted(user)" class="mini-tag green">积分扩容</span></span></div>
          <div class="user-risk"><span class="user-k">风控</span><button type="button" :class="['risk-chip', riskTone(user)]" :title="riskTitle(user)" :disabled="!manageable(user)" @click="riskUser = user"><AppIcon :name="riskTone(user) === 'ok' ? 'shield' : 'alert'" />{{ riskText(user) }}</button></div>
          <div class="user-actions"><button class="compact" @click="viewing = user"><AppIcon name="chart" />详情</button><button class="compact" :disabled="!manageable(user)" :title="manageable(user) ? '编辑用户' : '只有管理员可以编辑该账号'" @click="edit(user)"><AppIcon :name="manageable(user) ? 'edit' : 'lock'" />编辑</button></div>
        </li>
      </ul>
      <div v-if="!loading && !data?.items.length" class="empty-state compact-empty"><AppIcon name="users" /><p>{{ filtering ? '没有符合筛选条件的用户' : '暂无用户' }}</p><button v-if="filtering" type="button" @click="clearFilters">清除筛选</button></div>
      <div class="pagination"><span class="small muted">{{ loading ? '正在读取…' : `共 ${data?.total ?? 0} 个用户` }}</span><PageNumbers v-model="page" :pages="Math.max(1, Math.ceil((data?.total ?? 0) / 20))" :disabled="loading" previous-label="上一页用户" next-label="下一页用户" /></div>
    </div>
    <Modal v-if="open" :title="editing ? '编辑用户' : '创建用户'" :busy="saving" @close="open = false">
      <form class="user-form" @submit.prevent="save"><fieldset :disabled="saving || paused">
        <div v-if="editing" class="tip-head"><UserAvatar :id="editing.id" :name="editing.display_name || editing.username" :version="editing.avatar_version" :size="44" /><div><strong>{{ editing.display_name || editing.username }}</strong><p class="small muted">@{{ editing.username }} · {{ levelLabel(userCommunity(editing).level) }}</p></div></div>
        <h3 class="form-section">账号</h3>
        <div class="form-grid"><label class="form-field"><span class="field-label">用户名</span><input v-model="form.username" required maxlength="80" :disabled="!!editing" autocomplete="off" /></label><label class="form-field"><span class="field-label">显示名称 <small class="muted">可选</small></span><input v-model="form.display_name" maxlength="80" /></label></div>
        <label v-if="editing" class="form-field"><span class="field-label">邮箱 <small class="muted">可选</small></span><input v-model="form.email" type="email" maxlength="254" autocomplete="off" placeholder="未绑定" /><span :class="['field-hint', { 'danger-text': !emailOk }]">{{ emailOk ? '管理员设置的邮箱直接视为已验证，不受后缀限制；清空即解除绑定。' : '邮箱格式不正确' }}</span></label>
        <label class="form-field"><span class="field-label">{{ editing ? '重置密码' : '密码' }} <small v-if="editing" class="muted">可选</small></span><input v-model="form.password" type="password" :required="!editing" minlength="8" maxlength="1024" autocomplete="new-password" /><span class="field-hint">{{ editing ? '留空保留当前密码；重置后原会话失效。' : '至少 8 位，请通过可信方式交给用户。' }}</span></label>
        <h3 class="form-section">角色</h3>
        <div class="role-options" role="radiogroup" aria-label="角色"><label v-for="option in roleOptions" :key="option.value" :class="['role-option', { active: form.role === option.value, disabled: roleLocked && !!editing }]"><input v-model="form.role" type="radio" name="role" :value="option.value" :disabled="(roleLocked && !!editing) || (!isAdmin && option.value !== 'user')" /><strong>{{ option.label }}</strong><small>{{ option.hint }}</small></label></div>
        <p v-if="editing && roleLocked" class="field-hint">{{ !isAdmin ? '只有管理员可以设置角色。' : '不能修改初始管理员或自己的角色。' }}</p>
        <label v-if="editing && editing.id !== 1" class="switch-row"><input v-model="form.enabled" type="checkbox" class="switch" /><span><strong>启用账号</strong><small>停用后该账号无法登录，订阅停止输出。</small></span></label>
        <template v-if="editing"><h3 class="form-section">等级与积分</h3>
        <div class="form-grid"><label class="form-field"><span class="field-label">经验 XP</span><input v-model.number="form.xp" type="number" min="0" :max="Number.MAX_SAFE_INTEGER" step="1" required /><span class="field-hint">等级由经验计算：{{ resultingLevel }}</span></label><label class="form-field"><span class="field-label">积分</span><input v-model.number="form.points" type="number" min="0" :max="Number.MAX_SAFE_INTEGER" step="1" required /></label></div></template>
        <h3 class="form-section">额度</h3>
        <label class="switch-row"><input v-model="form.custom" type="checkbox" class="switch" /><span><strong>单独设置额度</strong><small>{{ form.custom ? '实际额度取等级默认与下方数值中较高者，再加积分扩容。' : '跟随等级默认额度，等级提升后自动增加。' }}</small></span></label>
        <div v-if="form.custom" class="form-grid quota-fields"><label class="form-field"><span class="field-label">节点</span><input v-model.number="form.node_limit" required type="number" min="1" max="100000" /></label><label class="form-field"><span class="field-label">来源</span><input v-model.number="form.source_limit" required type="number" min="1" max="10000" /></label><label class="form-field"><span class="field-label">订阅</span><input v-model.number="form.subscription_limit" required type="number" min="1" max="1000" /></label></div>
        <div v-else class="quota-preview"><div><span>节点</span><strong>{{ levelQuota(editing)?.nodes ?? '默认' }}</strong></div><div><span>来源</span><strong>{{ levelQuota(editing)?.sources ?? '默认' }}</strong></div><div><span>订阅</span><strong>{{ levelQuota(editing)?.subscriptions ?? '默认' }}</strong></div></div>
      </fieldset><p v-if="formError" class="notice error" role="alert">{{ formError }}</p><div class="dialog-actions"><button type="button" :disabled="saving" @click="open = false">取消</button><button class="primary" :disabled="saving || !valid || paused">{{ saving ? '保存中…' : '保存用户' }}</button></div></form>
    </Modal>
    <UserDetailDialog v-if="viewing" :user="viewing" :manageable="manageable(viewing)" @close="viewing = null" @edit="editFromDetail" @changed="load" />
    <Modal v-if="riskUser" title="风控详情" :busy="riskBusy" @close="riskUser = null">
      <div class="tip-head"><UserAvatar :id="riskUser.id" :name="riskUser.display_name || riskUser.username" :version="riskUser.avatar_version" :size="40" /><div><strong>{{ riskUser.display_name || riskUser.username }}</strong><p class="small muted">@{{ riskUser.username }}</p></div></div>
      <template v-if="risk(riskUser)">
        <div class="wallet-stats risk-stats"><div><span>{{ risk(riskUser)!.window_days }} 天内节点</span><strong>{{ risk(riskUser)!.window_nodes }}<small> / {{ risk(riskUser)!.window_limit }}</small></strong></div><div><span>今日新增</span><strong>{{ risk(riskUser)!.today_new }}<small> / {{ risk(riskUser)!.daily_new_cap }}</small></strong></div><div><span>{{ risk(riskUser)!.window_days }} 天内跟随来源</span><strong>{{ risk(riskUser)!.window_sources ?? 0 }}<small> / {{ risk(riskUser)!.source_window_limit ?? 0 }}</small></strong></div><div><span>今日新增来源</span><strong>{{ risk(riskUser)!.today_new_sources ?? 0 }}<small> / {{ risk(riskUser)!.source_daily_cap ?? 0 }}</small></strong></div><div><span>今日拉取 IP</span><strong>{{ risk(riskUser)!.today_ips }}<small> / {{ risk(riskUser)!.distinct_ips_threshold }}</small></strong></div><div><span>今日拉取次数</span><strong>{{ risk(riskUser)!.today_pulls }}</strong></div></div>
        <p v-if="risk(riskUser)!.flags?.length" class="notice warning">{{ risk(riskUser)!.flags!.map(f => flagNames[f] ?? f).join('、') }}<template v-if="risk(riskUser)!.frozen_until">，冻结至 {{ formatDate(risk(riskUser)!.frozen_until) }}</template>。</p>
        <p v-else class="small muted">没有异常标记。</p>
        <p class="small muted">风控不会自动封禁账号：超过新增上限时只是拒绝保存，多 IP 拉取只做标记。冻结需要人工操作，期间不能向订阅加入新节点，已有节点照常输出；重置窗口会清空近期节点领取记录。</p>
      </template>
      <p v-if="riskError" class="notice error" role="alert">{{ riskError }}</p>
      <div class="dialog-actions wrap"><button type="button" :disabled="riskBusy" @click="riskAction({ reset_exposure: true }, '已重置该用户的节点领取窗口')">重置窗口</button><button v-if="risk(riskUser)?.frozen_until" type="button" :disabled="riskBusy" @click="riskAction({ freeze_hours: 0 }, '已解除冻结')">解除冻结</button><button type="button" class="danger" :disabled="riskBusy" @click="riskAction({ freeze_hours: 24 }, '已冻结 24 小时')">冻结 24 小时</button><button type="button" class="danger" :disabled="riskBusy" @click="riskAction({ freeze_hours: 168 }, '已冻结 7 天')">冻结 7 天</button></div>
    </Modal>
  </section>
</template>
