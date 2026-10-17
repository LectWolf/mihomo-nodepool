<script setup lang="ts">
import { toastRef } from '../toast'
import { computed, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi } from '../api/system'
import { absoluteURL, accountTab, authOptions, clearPasswordSetup, copyText, currentUser, loadProfile, profile, limits } from '../api/accounts'
import { identityApi, oauthHref, providerNames, type Identity } from '../api/identity'
import { ledgerKinds, removeAvatar, squareAvatar, uploadAvatar, walletApi, type LedgerPage, type Wallet } from '../api/economy'
import { levelLabel, levelNames, timeAgo, useAccountScope } from '../community'
import AppIcon from './AppIcon.vue'
import TitleChip from './TitleChip.vue'
import LeaseCheckin from './LeaseCheckin.vue'
import ModeGuide from './ModeGuide.vue'
import PageNumbers from './PageNumbers.vue'
import UserAvatar from './UserAvatar.vue'
import EmailBindForm from './EmailBindForm.vue'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const displayName = ref(currentUser.value?.display_name ?? '')
const password = ref('')
const currentPassword = ref('')
// 旧站迁移或第三方注册的账号没有密码，可以直接设置
const hasPassword = computed(() => currentUser.value?.has_password !== false)
const confirmPassword = ref('')
const saving = ref('')
const error = ref('')
const message = toastRef()
const wallet = ref<Wallet | null>(null)
const ledger = ref<LedgerPage | null>(null)
const ledgerPage = ref(1)
const shopBusy = ref('')
const avatarBusy = ref(false)
const avatarInput = ref<HTMLInputElement | null>(null)
const rules = computed(() => wallet.value?.rules)
async function loadWallet() {
  const ticket = scope.ticket()
  try { const w = await walletApi.load(); if (scope.current(ticket)) wallet.value = w } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
}
async function loadLedger() {
  const ticket = scope.ticket()
  try { const v = await walletApi.ledger(ledgerPage.value, 8); if (scope.current(ticket)) ledger.value = v } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
}
async function load() {
  if (props.paused) return
  const ticket = scope.ticket()
  try { await loadProfile(); if (scope.current(ticket)) { displayName.value = currentUser.value?.display_name ?? ''; error.value = '' } }
  catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  await Promise.all([loadWallet(), loadLedger()])
}
watch(() => props.paused, paused => { if (!paused) { void load(); if (accountTab.value === 'security') void loadIdentities() } else { password.value = ''; confirmPassword.value = ''; currentPassword.value = '' } }, { immediate: true })
const tabs = [{ id: 'overview', label: '概览', icon: 'chart' }, { id: 'security', label: '资料与安全', icon: 'shield' }, { id: 'points', label: '积分与兑换', icon: 'coin' }] as const
watch(accountTab, tab => { if (tab === 'security' && !props.paused) void loadIdentities() })
// 第三方账号绑定
const identities = ref<Identity[]>([])
const providers = ref<Record<string, boolean>>({})
const emailEditing = ref(false)
const unlinking = ref('')
async function loadIdentities() {
  const ticket = scope.ticket()
  try { const v = await identityApi.identities(); if (scope.current(ticket)) { identities.value = v.items; providers.value = v.providers } }
  catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
}
const linkRows = computed(() => (['github', 'linuxdo'] as const).map(id => ({ id, name: providerNames[id], icon: id, bound: identities.value.find(v => v.provider === id), enabled: !!providers.value[id] })))
async function unlink(id: string) {
  if (unlinking.value) return
  unlinking.value = id
  try { await identityApi.unlink(id); message.value = `已解绑 ${providerNames[id]}`; await loadIdentities() }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { unlinking.value = '' }
}
function bindHref(id: string) { return oauthHref(id, 'bind') }
function emailBound() { emailEditing.value = false; void loadProfile() }
watch(ledgerPage, () => void loadLedger())
async function patch(kind: 'profile' | 'password' | 'beacon', body: Record<string, unknown>, done: string) {
  if (saving.value || props.paused) return
  const ticket = scope.ticket()
  saving.value = kind; error.value = ''; message.value = ''
  try {
    await api('/me', json('PATCH', body))
    if (!scope.current(ticket)) return
    message.value = done
    if (kind === 'password') { password.value = ''; confirmPassword.value = ''; currentPassword.value = ''; clearPasswordSetup() }
    await load()
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { saving.value = '' }
}
const beaconOn = computed(() => currentUser.value?.beacon_enabled !== false)
function toggleBeacon() { void patch('beacon', { beacon_enabled: !beaconOn.value }, beaconOn.value ? '已关闭实测回报，客户端下次更新订阅后生效。' : '已开启实测回报，客户端下次更新订阅后生效。') }
function saveProfile() { void patch('profile', { display_name: displayName.value.trim() }, '昵称已保存。') }
function savePassword() {
  if (hasPassword.value && !currentPassword.value) { error.value = '请输入当前密码。'; return }
  if (password.value.length < 8) { error.value = '新密码至少 8 位。'; return }
  if (password.value !== confirmPassword.value) { error.value = '两次输入的密码不一致。'; return }
  const body: Record<string, string> = { display_name: currentUser.value?.display_name ?? displayName.value.trim(), password: password.value }
  if (hasPassword.value) body.current_password = currentPassword.value
  void patch('password', body, hasPassword.value ? '密码已更新，其他设备需要重新登录。' : '登录密码已设置，之后可以用用户名或邮箱加密码登录。')
}
async function copy() {
  try { await copyText(absoluteURL(profile.value?.provider_path || `/nodes?provider_id=${currentUser.value?.id}`)); message.value = '提供方入口已复制。' }
  catch (cause) { error.value = errorText(cause) }
}
async function pickAvatar(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]; input.value = ''
  if (!file || avatarBusy.value) return
  avatarBusy.value = true; error.value = ''; message.value = ''
  try { await uploadAvatar(await squareAvatar(file)); message.value = '头像已更新。' }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { avatarBusy.value = false }
}
async function clearAvatar() {
  if (avatarBusy.value) return
  avatarBusy.value = true
  try { await removeAvatar(); message.value = '头像已移除。' } catch (cause) { error.value = errorText(cause) } finally { avatarBusy.value = false }
}
async function shop(key: string, action: () => Promise<Wallet>, done: string) {
  if (shopBusy.value || props.paused) return
  shopBusy.value = key; error.value = ''; message.value = ''
  try { wallet.value = await action(); message.value = done; await Promise.all([loadProfile(), loadLedger()]) }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { shopBusy.value = '' }
}
const boostAction = (kind: string) => () => walletApi.boost(kind)
const equipAction = (name: string) => () => walletApi.equipTitle(name)
const buyAction = (name: string) => () => walletApi.buyTitle(name)
const makeupAction = () => walletApi.makeup()
const leaveAction = () => walletApi.leave()
const points = computed(() => currentUser.value?.points ?? 0)
const expiresText = computed(() => { const v = currentUser.value?.subscription_expires_at; return v ? formatDate(v) : '未开通' })
const emailText = computed(() => currentUser.value?.email || '未绑定')
const boostCount = (kind: string) => wallet.value?.boosts.filter(b => b.kind === kind).length ?? 0
const boosts = computed(() => {
  const r = rules.value
  if (!r) return []
  return [
    { kind: 'node', label: '节点额度', gain: `+${r.boost_node_amount} 个节点`, price: r.boost_node_price },
    { kind: 'source', label: '来源额度', gain: '+1 个来源', price: r.boost_source_price },
    { kind: 'subscription', label: '订阅额度', gain: '+1 个组合订阅', price: r.boost_subscription_price },
  ]
})
const quotaRows = computed(() => {
  const l = wallet.value?.limits ?? profile.value?.limits
  const usage = profile.value?.usage
  const rows = [
    { key: 'nodes', label: '节点', used: usage?.nodes ?? 0, total: limits.value.nodes },
    { key: 'sources', label: '来源', used: usage?.sources ?? 0, total: limits.value.sources },
    { key: 'subscriptions', label: '订阅', used: usage?.subscriptions ?? 0, total: limits.value.subscriptions },
  ] as const
  return rows.map(row => ({ ...row, level: l?.level[row.key] ?? row.total, personal: l?.personal[row.key] ?? 0, boost: l?.boost[row.key] ?? 0, percent: row.total ? Math.min(100, Math.round(row.used / row.total * 100)) : 0 }))
})
const economyGuide = computed(() => {
  const r = rules.value
  if (!r) return []
  return [
    { icon: 'check', tone: 'tone-green', title: '签到：所有人的基础来源', text: `北京时间每天网页签到 +5 XP / +2 积分，连续每满 7 天再 +20 XP / +8 积分。客户端有效探测自动续期每天 +${r.probe_xp} XP。漏签可用积分补签保住连续天数。` },
    { icon: 'users', tone: 'tone-blue', title: '共享：提供方的主要来源', text: `每天结算：来源每有 1 位其他用户在用 +${r.share_xp_per_user} XP（单来源最多 ${r.share_xp_source_cap} 人，每日最多 ${r.share_xp_daily_cap} XP），并获得经验 ${r.share_points_percent}% 的积分。近 24 小时健康节点 ≥${r.share_quality_full}% 全额、≥${r.share_quality_half}% 减半，更低不计；一半以上节点为平台独有时再 +${r.unique_bonus_percent}%。` },
    { icon: 'star', tone: 'tone-amber', title: '里程碑与成就', text: `来源连续健康共享 ${r.milestone_days.join(' / ')} 天分别 +${r.milestone_xp.join(' / ')} XP；首次共享来源、首次有人使用你的来源、创建第一个订阅各 +${r.achievement_xp} XP，每项只发一次。` },
    { icon: 'coin', tone: 'tone-violet', title: '积分能做什么', text: `补签 ${r.makeup_price} 积分/天（每月 ${r.makeup_monthly} 天）、请假延期 ${r.leave_price} 积分 +${r.leave_days} 天（每月 ${r.leave_monthly} 次）、临时扩容 ${r.boost_days} 天、兑换称号，或打赏你喜欢的提供方。积分不能买经验和等级，也不能插队领取席位。` },
  ]
})
const achievementList = [
  { key: 'first_source', label: '首次共享来源' },
  { key: 'first_user', label: '首位使用者' },
  { key: 'first_subscription', label: '第一个订阅' },
]
</script>
<template>
  <section class="account-page">
    <div class="page-heading"><div><p class="eyebrow">MY ACCOUNT</p><h1>我的账号</h1><p class="muted">等级、额度、积分和个人资料都在这里。</p></div></div>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <section class="panel account-hero">
      <div class="profile-avatar"><UserAvatar :id="currentUser?.id" :name="currentUser?.display_name || currentUser?.username" :version="currentUser?.avatar_version" :size="76" /><button type="button" class="icon-button avatar-edit" aria-label="更换头像" title="更换头像" :disabled="avatarBusy" @click="avatarInput?.click()"><span v-if="avatarBusy" class="spinner"></span><AppIcon v-else name="camera" /></button><input ref="avatarInput" class="sr-only" type="file" accept="image/png,image/jpeg,image/webp,image/gif" @change="pickAvatar" /></div>
      <div class="profile-copy">
        <h2>{{ currentUser?.display_name || currentUser?.username }}<TitleChip :name="currentUser?.title" /></h2>
        <p class="account-meta"><span>@{{ currentUser?.username }}</span><span class="level-pill">{{ levelLabel(currentUser?.level ?? 0) }}</span><span :class="['mini-tag', currentUser?.email ? 'tone-green' : 'tone-amber']"><AppIcon name="mail" />{{ currentUser?.email ? '邮箱已验证' : '未绑定邮箱' }}</span></p>
      </div>
      <dl class="account-stats"><div><dt>积分</dt><dd>{{ points.toLocaleString() }}</dd></div><div><dt>经验</dt><dd>{{ (currentUser?.xp ?? 0).toLocaleString() }}</dd></div><div><dt>连续签到</dt><dd>{{ currentUser?.checkin_streak ?? 0 }}<small> 天</small></dd></div><div><dt>订阅有效期</dt><dd class="account-expire">{{ expiresText }}</dd></div></dl>
    </section>
    <div class="scope-tabs account-tabs" role="tablist" aria-label="账号分区"><button v-for="t in tabs" :key="t.id" type="button" role="tab" :aria-selected="accountTab === t.id" :aria-pressed="accountTab === t.id" @click="accountTab = t.id"><AppIcon :name="t.icon" />{{ t.label }}</button></div>

    <template v-if="accountTab === 'overview'">
      <LeaseCheckin :paused="paused" @renewed="loadWallet" />
      <div class="account-grid">
        <section class="panel quota-card">
          <div class="panel-heading"><div><h2>账号额度</h2><p class="small muted">实际额度 = 等级默认与管理员设置取较高者 + 临时扩容。未设置使用人数上限的来源不占来源额度。</p></div></div>
          <div v-for="row in quotaRows" :key="row.key" class="quota-row">
            <div class="quota-line"><span>{{ row.label }}</span><strong>{{ row.used }}<small> / {{ row.total }}</small></strong></div>
            <div class="seat-bar"><span :style="{ width: `${row.percent}%` }" :data-full="row.percent >= 100"></span></div>
            <small class="muted">等级 {{ row.level }} · 管理员 {{ row.personal }}<template v-if="row.boost"> · 扩容 +{{ row.boost }}</template></small>
          </div>
          <details v-if="rules" class="level-thresholds"><summary class="small">各等级默认额度</summary><table class="quota-table"><thead><tr><th>等级</th><th>节点</th><th>来源</th><th>订阅</th></tr></thead><tbody><tr v-for="(q, index) in rules.level_quotas" :key="index" :class="{ current: index === (currentUser?.level ?? 0) }"><td>Lv{{ index }} {{ levelNames[index] }}</td><td>{{ q.nodes }}</td><td>{{ q.sources }}</td><td>{{ q.subscriptions }}</td></tr></tbody></table></details>
        </section>
        <section class="panel achievements-card">
          <div class="panel-heading"><div><h2>今日与成就</h2><p class="small muted">共享奖励每天北京时间零点后结算一次。</p></div></div>
          <div class="wallet-stats"><div><span>可用积分</span><strong>{{ points.toLocaleString() }}</strong></div><div><span>今日共享经验</span><strong>{{ wallet?.today_share_xp ?? 0 }}</strong></div><div><span>本月补签</span><strong>{{ wallet?.makeup_used ?? 0 }}<small> / {{ rules?.makeup_monthly ?? 0 }}</small></strong></div><div><span>本月请假</span><strong>{{ wallet?.leave_used ?? 0 }}<small> / {{ rules?.leave_monthly ?? 0 }}</small></strong></div></div>
          <ul class="achievement-list"><li v-for="item in achievementList" :key="item.key" :class="{ done: wallet?.achievements[item.key] }"><AppIcon :name="wallet?.achievements[item.key] ? 'check' : 'star'" />{{ item.label }}<small>+{{ rules?.achievement_xp ?? 0 }} XP</small></li></ul>
        </section>
      </div>
      <section class="panel provider-entry">
        <div class="panel-heading"><div><h2>我的提供方入口</h2><p class="small muted">分享这个入口，其他用户登录后可查看和领取你提供的节点。</p></div></div>
        <div class="copy-field"><input aria-label="我的提供方入口" :value="absoluteURL(profile?.provider_path || `/nodes?provider_id=${currentUser?.id}`)" readonly /><button type="button" class="icon-button" aria-label="复制提供方入口" title="复制提供方入口" @click="copy"><AppIcon name="copy" /></button></div>
      </section>
      <ModeGuide storage-key="nodepool-guide-economy" title="经验与积分怎么来、怎么用" summary="经验决定等级与额度，积分用来补签、请假、扩容和打赏。" :items="economyGuide" />
    </template>

    <div v-else-if="accountTab === 'security'" class="security-grid">
      <div class="security-col">
      <form class="panel security-card" @submit.prevent="saveProfile">
        <div class="panel-heading"><div><h2>基本资料</h2><p class="small muted">昵称会显示在提供方、来源和订阅用户列表中。</p></div></div>
        <fieldset :disabled="!!saving || paused">
          <label class="form-field"><span class="field-label">昵称</span><input v-model="displayName" maxlength="40" required /></label>
          <label class="form-field"><span class="field-label">用户名</span><div class="readonly-field"><AppIcon name="lock" /><span>{{ currentUser?.username }}</span></div><span class="field-hint">用户名是登录凭据，注册后不可修改。</span></label>
          <div class="form-field"><span class="field-label">头像</span><div class="avatar-row"><UserAvatar :id="currentUser?.id" :name="currentUser?.display_name || currentUser?.username" :version="currentUser?.avatar_version" :size="40" /><button type="button" class="compact" :disabled="avatarBusy" @click="avatarInput?.click()">更换</button><button v-if="(currentUser?.avatar_version ?? 0) > 0" type="button" class="compact ghost" :disabled="avatarBusy" @click="clearAvatar">移除</button></div><span class="field-hint">自动裁剪为正方形；动图会转为静态图片，最大 200KB。</span></div>
        </fieldset>
        <div class="dialog-actions"><button class="primary" :disabled="!!saving || paused || !displayName.trim() || displayName.trim() === currentUser?.display_name">{{ saving === 'profile' ? '保存中…' : '保存昵称' }}</button></div>
      </form>
      <form class="panel security-card" @submit.prevent="savePassword">
        <div class="panel-heading"><div><h2>{{ hasPassword ? '登录密码' : '设置登录密码' }}</h2><p class="small muted">{{ hasPassword ? '需要验证当前密码，修改后其他设备上的登录会失效。' : '当前账号还没有登录密码，设置后可以用用户名或邮箱加密码登录。' }}</p></div></div>
        <fieldset :disabled="!!saving || paused">
          <input class="sr-only" :value="currentUser?.username" autocomplete="username" readonly tabindex="-1" aria-hidden="true" />
          <label v-if="hasPassword" class="form-field"><span class="field-label">当前密码</span><input v-model="currentPassword" type="password" maxlength="1024" autocomplete="current-password" placeholder="请输入当前密码" /><span v-if="currentUser?.email_verified" class="field-hint">忘记当前密码时，可以退出登录后在登录页使用“忘记密码”通过邮箱重置。</span></label>
          <label class="form-field"><span class="field-label">新密码</span><input v-model="password" type="password" minlength="8" maxlength="1024" autocomplete="new-password" placeholder="至少 8 位" /></label>
          <label class="form-field"><span class="field-label">确认新密码</span><input v-model="confirmPassword" type="password" minlength="8" maxlength="1024" autocomplete="new-password" placeholder="再输入一次" /></label>
        </fieldset>
        <div class="dialog-actions"><button class="primary" :disabled="!!saving || paused || !password || (hasPassword && !currentPassword)">{{ saving === 'password' ? '保存中…' : hasPassword ? '更新密码' : '设置密码' }}</button></div>
      </form>
      </div>
      <div class="security-col">
      <section class="panel security-card">
        <div class="panel-heading"><div><h2>邮箱</h2><p class="small muted">用于登录、找回密码和接收通知。</p></div></div>
        <div class="security-row"><span class="security-icon"><AppIcon name="mail" /></span><div><strong>{{ emailText }}</strong><small :class="currentUser?.email ? 'green-text' : 'muted'">{{ currentUser?.email ? '已验证' : authOptions.login.email ? '尚未绑定' : '管理员未配置邮件服务' }}</small></div><button v-if="authOptions.login.email && !emailEditing" type="button" class="compact" @click="emailEditing = true">{{ currentUser?.email ? '换绑' : '绑定' }}</button></div>
        <EmailBindForm v-if="emailEditing" :current="currentUser?.email" @bound="emailBound"><template #extra><button type="button" class="ghost" @click="emailEditing = false">取消</button></template></EmailBindForm>
      </section>
      <section class="panel security-card">
        <div class="panel-heading"><div><h2>第三方账号</h2><p class="small muted">绑定后可以直接用第三方账号登录。</p></div></div>
        <ul class="link-list"><li v-for="row in linkRows" :key="row.id"><span :class="['security-icon', row.id]"><AppIcon :name="row.icon" /></span><div><strong>{{ row.name }}</strong><small class="muted">{{ row.bound ? `已绑定 ${row.bound.login || ''}` : row.enabled ? '未绑定' : '管理员未启用' }}</small></div>
          <button v-if="row.bound" type="button" class="compact ghost" :disabled="!!unlinking" @click="unlink(row.id)">{{ unlinking === row.id ? '解绑中…' : '解绑' }}</button>
          <a v-else-if="row.enabled" class="button compact" :href="bindHref(row.id)">绑定</a>
        </li></ul>
      </section>
      <section class="panel security-card">
        <div class="panel-heading"><div><h2>节点实测回报</h2><p class="small muted">让大家知道节点在国内到底能不能连。</p></div><label class="switch-field"><span>{{ beaconOn ? '已开启' : '已关闭' }}</span><button type="button" :class="['switch', { on: beaconOn }]" role="switch" :aria-checked="beaconOn" aria-label="节点实测回报" :disabled="!!saving || paused" @click="toggleBeacon"><span></span></button></label></div>
        <ul class="beacon-notes">
          <li>Clash 客户端每 3 小时经订阅里最多 15 个节点访问一次本站，每次约 1 KB，只记录哪个节点能连上，不涉及浏览内容。</li>
          <li>结果显示在节点卡片的“国内实测”上；你的客户端连不上的节点，会自动排到你订阅的末尾。</li>
          <li>关闭后客户端下次更新订阅时生效，网站上仍能看到其他人的实测数据。</li>
        </ul>
      </section>
      </div>
    </div>

    <template v-else>
      <section v-if="rules" class="panel shop-panel">
        <div class="panel-heading"><div><h2>积分兑换</h2><p class="small muted">兑换立即生效并记入积分明细。积分不足或超出次数时不会扣除。</p></div><span class="points-pill"><AppIcon name="coin" />{{ points.toLocaleString() }}</span></div>
        <div class="shop-grid">
          <article class="shop-item"><div><strong>补签</strong><p class="small muted">{{ wallet?.missed_days ? `漏签 ${wallet.missed_days} 天，补签后保住连续签到` : '昨天已签到或尚未开始连续签到' }}</p></div><div class="shop-buy"><span>{{ rules.makeup_price }} 积分/天</span><button class="compact" :disabled="!!shopBusy || !wallet?.missed_days" @click="shop('makeup', makeupAction, '补签成功，连续签到已保住。')">{{ shopBusy === 'makeup' ? '处理中…' : wallet?.missed_days ? `补签 ${wallet.missed_days} 天` : '无需补签' }}</button></div></article>
          <article class="shop-item"><div><strong>请假延期</strong><p class="small muted">出门在外暂时不能签到，订阅与席位到期时间顺延 {{ rules.leave_days }} 天。</p></div><div class="shop-buy"><span>{{ rules.leave_price }} 积分</span><button class="compact" :disabled="!!shopBusy" @click="shop('leave', leaveAction, `已请假，订阅顺延 ${rules.leave_days} 天。`)">{{ shopBusy === 'leave' ? '处理中…' : '请假' }}</button></div></article>
          <article v-for="b in boosts" :key="b.kind" class="shop-item"><div><strong>临时{{ b.label }}</strong><p class="small muted">{{ b.gain }}，{{ rules.boost_days }} 天有效 · 已生效 {{ boostCount(b.kind) }} / {{ rules.boost_max_active }}</p></div><div class="shop-buy"><span>{{ b.price }} 积分</span><button class="compact" :disabled="!!shopBusy || boostCount(b.kind) >= rules.boost_max_active" @click="shop(b.kind, boostAction(b.kind), `${b.label}已扩容。`)">{{ shopBusy === b.kind ? '处理中…' : '扩容' }}</button></div></article>
        </div>
        <ul v-if="wallet?.boosts.length" class="boost-list"><li v-for="(b, index) in wallet.boosts" :key="index"><span>{{ b.kind === 'node' ? '节点' : b.kind === 'source' ? '来源' : '订阅' }} +{{ b.amount }}</span><small class="muted">至 {{ formatDate(b.expires_at) }}</small></li></ul>
        <h3 class="shop-subtitle">称号</h3>
        <div class="title-grid"><article v-for="t in rules.titles" :key="t.name" class="title-item" :class="{ owned: wallet?.titles.includes(t.name), equipped: currentUser?.title === t.name }"><TitleChip :name="t.name" :color="t.color || 'amber'" large /><small class="muted">{{ wallet?.titles.includes(t.name) ? '已拥有' : `${t.price} 积分` }}</small>
          <button v-if="currentUser?.title === t.name" class="compact" :disabled="!!shopBusy" @click="shop('title', equipAction(''), '已取下称号。')">取下</button>
          <button v-else-if="wallet?.titles.includes(t.name)" class="compact" :disabled="!!shopBusy" @click="shop('title', equipAction(t.name), `已佩戴「${t.name}」。`)">佩戴</button>
          <button v-else class="compact" :disabled="!!shopBusy || points < t.price" @click="shop('title', buyAction(t.name), `已兑换并佩戴「${t.name}」。`)">兑换</button>
        </article></div>
      </section>
      <section class="panel ledger-panel">
        <div class="panel-heading"><div><h2>积分与经验明细</h2><p class="small muted">每一笔变动都有记录，包括管理员调整。</p></div></div>
        <ul v-if="ledger?.items.length" class="ledger-list"><li v-for="item in ledger.items" :key="item.id"><span class="ledger-kind">{{ ledgerKinds[item.kind] ?? item.kind }}</span><span class="ledger-note">{{ item.note }}</span><span class="ledger-values"><b v-if="item.xp" :class="item.xp > 0 ? 'green-text' : 'danger-text'">{{ item.xp > 0 ? '+' : '' }}{{ item.xp }} XP</b><b v-if="item.points" :class="item.points > 0 ? 'green-text' : 'danger-text'">{{ item.points > 0 ? '+' : '' }}{{ item.points }} 积分</b></span><small class="muted" :title="formatDate(item.created_at)">{{ timeAgo(item.created_at) }}</small></li></ul>
        <p v-else class="small muted">暂无记录，签到或共享来源后会显示在这里。</p>
        <PageNumbers v-if="(ledger?.total ?? 0) > 8" v-model="ledgerPage" :pages="Math.ceil((ledger?.total ?? 0) / 8)" label="明细分页" />
      </section>
    </template>
  </section>
</template>
