<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { abortRequests, apiEvents, errorText, isCancelled, json, useApi, type Source, type SystemInfo } from './api/system'
import { authOptions, authPrompt, canManageUsers, accountTab, clearAccount, clearPasswordSetup, currentUser, emailRequired, isAdmin, isGuest, loadProfile, passwordSetupPending, type AuthResponse } from './api/accounts'
import { providerNames } from './api/identity'
import { usePolling } from './polling'
import NodesView from './components/NodesView.vue'
import SourcesView from './components/SourcesView.vue'
import SettingsView from './components/SettingsView.vue'
import ProvidersView from './components/ProvidersView.vue'
import SubscriptionsView from './components/SubscriptionsView.vue'
import RulesView from './components/RulesView.vue'
import UsersView from './components/UsersView.vue'
import AccountView from './components/AccountView.vue'
import StatisticsView from './components/StatisticsView.vue'
import VersionsDialog from './components/VersionsDialog.vue'
import Modal from './components/Modal.vue'
import AppIcon from './components/AppIcon.vue'
import ThemeSelect from './components/ThemeSelect.vue'
import LayoutToggle from './components/LayoutToggle.vue'
import ClassicHome from './components/ClassicHome.vue'
import { classic } from './layout'
import ToastHost from './components/ToastHost.vue'
import UserMenu from './components/UserMenu.vue'
import AuthPanel from './components/AuthPanel.vue'
import EmailBindForm from './components/EmailBindForm.vue'
import PasswordSetupForm from './components/PasswordSetupForm.vue'
import OnboardingTour, { type TourStep } from './components/OnboardingTour.vue'
import { toast } from './toast'
import { cached, clearCache, remember } from './view-cache'
import { loadTitles } from './titles'
import { navigate, route, sectionHref, type Section } from './router'
const api = useApi()
const authenticated = ref(false)
const booting = ref(true)
const loginError = ref('')
const viewKey = ref(0)
const logoutBusy = ref(false)
const section = computed(() => route.value.section)
const visited = reactive(new Set<Section>([section.value]))
// short 为经典版顶部导航的简称；来源目录只在经典版出现
const allSections = [
  { id: 'catalog', label: '来源目录', short: '来源', icon: 'layers', admin: false },
  { id: 'nodes', label: '节点总览', short: '节点', icon: 'nodes', admin: false },
  { id: 'sources', label: '共享来源', short: '共享', icon: 'sources', admin: false },
  { id: 'providers', label: '提供方', short: '提供方', icon: 'users', admin: false },
  { id: 'subscriptions', label: '我的订阅', short: '订阅', icon: 'link', admin: false },
  { id: 'rules', label: '规则组合', short: '规则', icon: 'settings', admin: false },
  { id: 'account', label: '我的账号', short: '账号', icon: 'user', admin: false },
  { id: 'statistics', label: '运行统计', short: '统计', icon: 'chart', admin: false },
  { id: 'users', label: '用户管理', short: '用户', icon: 'users', admin: 'manage' },
  { id: 'settings', label: '运行设置', short: '设置', icon: 'settings', admin: true },
] as const
// 访客只能浏览来源、节点、提供方和运行统计
const guestSections: Section[] = ['catalog', 'nodes', 'providers', 'statistics']
const homeSection = computed<Section>(() => classic.value ? 'catalog' : 'nodes')
const sections = computed(() => (isGuest.value ? allSections.filter(item => guestSections.includes(item.id)) : allSections.filter(item => !item.admin || isAdmin.value || (item.admin === 'manage' && canManageUsers.value))).filter(item => classic.value || item.id !== 'catalog'))
const currentTitle = computed(() => sections.value.find(item => item.id === section.value)?.label)
watch([section, authenticated, isAdmin, canManageUsers, isGuest, classic], ([id, loggedIn, admin, manager, guest, classicLayout]) => {
  // 新版没有来源目录页，对应到节点总览的按来源视图
  if (id === 'catalog' && !classicLayout) { navigate('nodes', new URLSearchParams({ by: 'sources' }), true); return }
  if (guest && !guestSections.includes(id)) { const label = allSections.find(item => item.id === id)?.label; navigate(homeSection.value); authPrompt.value = { mode: 'login', reason: label ? `登录后即可使用「${label}」。` : undefined }; return }
  if (loggedIn && ((!admin && id === 'settings') || (!manager && id === 'users'))) { navigate(homeSection.value); return }
  visited.add(id)
}, { flush: 'sync' })
// 经典版手机端用抽屉菜单
const drawerOpen = ref(false)
watch([section, classic], () => { drawerOpen.value = false })
function drawerGo(event: MouseEvent, id: Section) { drawerOpen.value = false; navigateLink(event, id) }
function drawerKey(event: KeyboardEvent) { if (event.key === 'Escape') drawerOpen.value = false }
watch(drawerOpen, open => { if (open) document.addEventListener('keydown', drawerKey); else document.removeEventListener('keydown', drawerKey) })
// 手机底部导航：页面较多时保留常用 4 项，其余放进“更多”
const mobileMore = ref(false)
const mobilePrimary: Section[] = ['nodes', 'subscriptions', 'sources', 'account']
const mobileTabs = computed(() => sections.value.length <= 5 ? sections.value : sections.value.filter(item => mobilePrimary.includes(item.id)))
const mobileOverflow = computed(() => sections.value.filter(item => !mobileTabs.value.includes(item)))
const moreActive = computed(() => mobileOverflow.value.some(item => item.id === section.value))
function mobileGo(event: MouseEvent, id: Section) { mobileMore.value = false; navigateLink(event, id) }
function navigateLink(event: MouseEvent, id: Section) {
  if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return
  event.preventDefault(); navigate(id)
}
const system = ref<SystemInfo | null>(null)
const systemError = ref('')
const sources = ref<Source[]>([])
// 管理员默认只看自己的来源，切到全站时单独请求与缓存
const sourceScope = ref<'mine' | 'all'>('mine')
const sourcesPath = computed(() => sourceScope.value === 'all' && currentUser.value?.role === 'admin' ? '/sources?scope=all' : '/sources')
const ownSourceCount = computed(() => sources.value.filter(item => item.owner_id === currentUser.value?.id).length)
function setSourceScope(value: 'mine' | 'all') {
  if (value === sourceScope.value) return
  sourceScope.value = value; sourcesRevision++; sourcesError.value = ''
  sources.value = cached<Source[]>(sourcesPath.value) ?? []
  void loadSources()
}
const sourcesLoading = ref(false)
const sourcesError = ref('')
let sourcesFollowUp: ReturnType<typeof setTimeout> | undefined
function scheduleSourcesFollowUp() {
  if (sourcesFollowUp) return
  sourcesFollowUp = setTimeout(() => { sourcesFollowUp = undefined; void loadSources() }, 1500)
}
const databaseUnavailable = ref(false)
const passwordSetup = ref(false)
const versionsOpen = ref(false)
const versionKey = ref(0)
const reconnecting = ref(false)
const reconnectError = ref('')
let restartAt = 0
let reconnectTimer: ReturnType<typeof setTimeout> | undefined
let systemLoading = false
let connecting = false
let sourcesRevision = 0
let sessionRevision = 0
function updateSource(source: Source) { sourcesRevision++; sources.value = sources.value.map(item => item.id === source.id ? source : item) }
function removeSource(id: number) { sourcesRevision++; sources.value = sources.value.filter(item => item.id !== id) }
function clearSession() {
  clearCache(); sessionRevision++; abortRequests(); authenticated.value = false; clearAccount()
  system.value = null; sources.value = []; sourceScope.value = 'mine'; sourcesError.value = ''; systemError.value = ''
  versionsOpen.value = false; reconnecting.value = false; reconnectError.value = ''; isGuest.value = false; tourOpen.value = false; emailRequired.value = false; passwordSetup.value = false
  databaseUnavailable.value = false; sourcesRevision++; visited.clear(); visited.add(section.value); viewKey.value++
}
function unauthorized() {
  if (isGuest.value) { authPrompt.value ??= { mode: 'login' }; return }
  if (authenticated.value) loginError.value = '登录已过期或账号已停用，请重新登录。'
  const expired = authenticated.value
  clearSession()
  void loadAuth().then(() => { if (expired && isGuest.value) authPrompt.value = { mode: 'login' } })
}
function unavailable() { databaseUnavailable.value = true }
function needEmail() { if (authenticated.value) emailRequired.value = true }
// OAuth 回调通过查询参数带回结果，提示后清理地址栏
function readOAuthResult() {
  const q = new URLSearchParams(location.search)
  const failed = q.get('oauth_error'), bound = q.get('bound'), via = q.get('oauth')
  if (!failed && !bound && !via) return
  if (bound || (failed && location.pathname === '/account')) accountTab.value = 'security'
  if (failed) toast(failed, 'error')
  else if (bound) toast(`已绑定 ${providerNames[bound] ?? bound}`)
  else if (q.get('welcome')) toast(`已通过 ${providerNames[via ?? ''] ?? '第三方账号'} 注册，欢迎加入`)
  for (const key of ['oauth_error', 'bound', 'oauth', 'welcome']) q.delete(key)
  history.replaceState(history.state, '', `${location.pathname}${q.size ? `?${q}` : ''}${location.hash}`)
}
function passwordDone() { passwordSetup.value = false; clearPasswordSetup() }
async function emailBound() { emailRequired.value = false; await loadProfile().catch(() => undefined) }
function maintenance() { void loadSystem() }
function settingsSaved() { void loadSystem(); void loadTitles(true) }
const maintenanceOn = computed(() => !!system.value?.maintenance?.enabled)
async function loadSystem() {
  if ((!authenticated.value && !isGuest.value) || systemLoading) return
  systemLoading = true
  const revision = sessionRevision
  try {
    const value = await api<SystemInfo>('/system')
    if ((!authenticated.value && !isGuest.value) || revision !== sessionRevision) return
    system.value = value; remember('/system', value)
    if (value.database) databaseUnavailable.value = !value.database.connected
    systemError.value = ''
  } catch (error) { if (!isCancelled(error) && (authenticated.value || isGuest.value) && revision === sessionRevision) systemError.value = errorText(error) }
  finally { systemLoading = false }
}
async function loadSources() {
  if (!authenticated.value || sourcesLoading.value) return
  sourcesLoading.value = true
  const revision = sourcesRevision, path = sourcesPath.value
  try {
    const value = await api<{ items: Source[] }>(path)
    if (authenticated.value && revision === sourcesRevision) {
      sources.value = value.items ?? []
      sourcesError.value = ''
      databaseUnavailable.value = false
      remember(path, sources.value)
      // 有来源正在导入/刷新时短间隔跟进，空闲时完全依赖 15 秒主轮询。
      if (sources.value.some(source => source.status === 'queued' || source.status === 'processing')) scheduleSourcesFollowUp()
      else if (sourcesFollowUp) { clearTimeout(sourcesFollowUp); sourcesFollowUp = undefined }
    }
  } catch (error) { if (!isCancelled(error) && authenticated.value && revision === sourcesRevision) sourcesError.value = errorText(error) }
  finally {
    sourcesLoading.value = false
    if (authenticated.value && revision !== sourcesRevision) void loadSources()
  }
}
async function refreshOverview() { await Promise.all([loadSystem(), loadSources()]) }
function applyOptions(auth: AuthResponse) {
  authOptions.guestBrowse = !!auth.guest_browse; authOptions.registration = !!auth.registration
  if (auth.login_options) authOptions.login = { ...auth.login_options, email_suffixes: auth.login_options.email_suffixes ?? [] }
}
async function establishSession(auth: AuthResponse) {
  applyOptions(auth)
  if (!auth.authenticated || !auth.user) { applyGuest(auth); return }
  emailRequired.value = !!auth.email_required
  const wasGuest = isGuest.value
  if (wasGuest) { sessionRevision++; abortRequests(); visited.clear(); visited.add(section.value); viewKey.value++ }
  isGuest.value = false; authPrompt.value = null; loginError.value = ''
  currentUser.value = auth.user; authenticated.value = true
  // 通过第三方验证身份的无密码账号，登录后要求补设密码
  if (!sources.value.length) sources.value = cached<Source[]>('/sources') ?? []
  system.value ??= cached<SystemInfo>('/system') ?? null
  if (passwordSetupPending()) { passwordSetup.value = auth.user.has_password === false; if (!passwordSetup.value) clearPasswordSetup() }
  await Promise.all([refreshOverview(), loadProfile().catch(error => { if (!isCancelled(error) && authenticated.value) systemError.value = errorText(error) })])
  maybeStartTour()
}
// 未登录：允许访客浏览时进入只读模式，否则显示登录页
function applyGuest(auth: AuthResponse) {
  if (authenticated.value) clearSession()
  applyOptions(auth)
  isGuest.value = authOptions.guestBrowse
  if (isGuest.value) { void loadSystem(); void loadTitles(true) }
}
async function loadAuth() {
  try { await establishSession(await api<AuthResponse>('/auth')) }
  catch (error) { if (!isCancelled(error)) loginError.value = errorText(error) }
}
function openAuth(mode: 'login' | 'register') { authPrompt.value = { mode } }
async function authenticatedFromPanel(auth: AuthResponse) { authPrompt.value = null; await establishSession(auth) }
function authMode(mode: 'login' | 'register') { if (authPrompt.value) authPrompt.value = { ...authPrompt.value, mode } }
// 新手引导：每个账号首次登录自动显示一次，可在账号菜单重新查看
const tourOpen = ref(false)
const tourKey = () => `nodepool-tour-v1:${currentUser.value?.id ?? 0}`
function maybeStartTour() {
  let seen = true
  try { seen = !!localStorage.getItem(tourKey()) } catch { /* 隐私模式下不自动弹出 */ }
  if (!seen) startTour()
}
function startTour() { if (section.value !== 'nodes') navigate('nodes'); tourOpen.value = true }
function closeTour() { tourOpen.value = false; try { localStorage.setItem(tourKey(), new Date().toISOString()) } catch { /* 忽略 */ } }
const tourSteps = computed<TourStep[]>(() => {
  const steps: TourStep[] = [
    { title: '欢迎来到 MCII 节点共享', icon: 'compass', body: '这里汇集大家共享的机场订阅，自动去重并持续检测延迟。你可以挑选节点组合成自己的订阅，导入 Clash 等客户端使用。花一分钟认识一下各个页面吧。' },
    { target: '[data-tour="nav-nodes"]', icon: 'nodes', title: '节点总览', body: '浏览所有共享节点的延迟、地区和来源，支持按国家、协议、提供方筛选。', before: () => navigate('nodes') },
    { target: '[data-tour="select-nodes"]', icon: 'plus', title: '挑选节点组合订阅', body: '点「选择节点」进入选择模式，点击卡片勾选节点，也可以跟随整个来源（自动同步节点），最后保存为一条订阅。', before: () => navigate('nodes') },
    { target: '[data-tour="nav-subscriptions"]', icon: 'link', title: '我的订阅', body: '保存的组合会生成订阅链接：选择 Clash / Mihomo 格式后复制、一键导入或扫码。修改前的组合会保留在「历史组合」里，可随时恢复。' },
    { target: '[data-tour="nav-sources"]', icon: 'sources', title: '共享来源', body: '把自己的机场订阅共享出来，供大家使用；被使用会获得经验和积分，也能看到使用人数和流量趋势。' },
    { target: '[data-tour="nav-rules"]', icon: 'settings', title: '规则组合', body: '控制分流规则和策略组。官方规则开箱即用，会按订阅里的国家自动生成分组；也可以复制一份自己调整。' },
    { target: '[data-tour="nav-account"]', icon: 'user', title: '我的账号', body: '每天签到续期订阅、积累经验升级，等级越高可用的节点和来源额度越多。' },
    { target: '[data-tour="user-menu"]', icon: 'compass', title: '账号菜单', body: '右上角可以查看等级与积分、切换页面、退出登录；想再看一遍这个教程，也在这里点「新手教程」。' },
  ]
  return steps
})
async function logout() {
  if (logoutBusy.value) return
  logoutBusy.value = true
  try { await api('/auth/logout', json('POST')); clearPasswordSetup(); clearSession(); toast('已退出登录', 'info'); await loadAuth() }
  catch (error) { if (!isCancelled(error)) systemError.value = errorText(error) }
  finally { logoutBusy.value = false }
}
function startReconnect() {
  versionsOpen.value = false
  restartAt = Date.now()
  reconnecting.value = true
  reconnectError.value = ''
  scheduleReconnect()
}
// 重启后单独用 2 秒节奏探测，避免被 15 秒主轮询拖慢。
function scheduleReconnect() {
  if (reconnectTimer) return
  reconnectTimer = setTimeout(async () => {
    reconnectTimer = undefined
    if (!reconnecting.value) return
    await reconnect()
    if (reconnecting.value) scheduleReconnect()
  }, 2000)
}
async function reconnect() {
  if (connecting || Date.now() - restartAt < 3000) return
  if (Date.now() - restartAt > 120000) { reconnecting.value = false; reconnectError.value = '服务尚未恢复，请确认服务已启动，然后重新连接。'; return }
  connecting = true
  try {
    const auth = await api<AuthResponse>('/auth')
    if (!auth.authenticated) { clearSession(); applyGuest(auth); loginError.value = '服务已恢复，请重新登录。'; return }
    reconnecting.value = false; reconnectError.value = ''; versionKey.value++
    await establishSession(auth)
  } catch (error) { if (!isCancelled(error)) reconnectError.value = '正在等待服务恢复，稍后会自动重连…' }
  finally { connecting = false }
}
usePolling(async () => {
  if (reconnecting.value) await reconnect()
  else if (authenticated.value && !reconnectError.value) await refreshOverview()
  else if (isGuest.value) await loadSystem()
}, () => sources.value.some(source => source.status === 'queued' || source.status === 'processing') ? 3000 : 15000, false)
onMounted(async () => {
  apiEvents.addEventListener('unauthorized', unauthorized)
  apiEvents.addEventListener('database-unavailable', unavailable)
  apiEvents.addEventListener('maintenance', maintenance)
  apiEvents.addEventListener('email-required', needEmail)
  readOAuthResult()
  try { await loadAuth() }
  finally { booting.value = false }
})
onBeforeUnmount(() => {
  apiEvents.removeEventListener('unauthorized', unauthorized)
  apiEvents.removeEventListener('database-unavailable', unavailable)
  apiEvents.removeEventListener('maintenance', maintenance)
  apiEvents.removeEventListener('email-required', needEmail)
  document.removeEventListener('keydown', drawerKey)
  if (sourcesFollowUp) clearTimeout(sourcesFollowUp)
  if (reconnectTimer) clearTimeout(reconnectTimer)
})
watch(authenticated, on => { if (on) void loadTitles(true) })
</script>

<template>
  <ThemeSelect v-if="!authenticated && !isGuest" class="guest-theme" />
  <ToastHost />
  <div v-if="booting" class="boot-screen" role="status"><span class="brand-symbol">M</span><span class="spinner"></span>正在连接 MCII节点共享…</div>
  <main v-else-if="!authenticated && !isGuest" class="login-screen">
    <div class="login-intro"><span class="brand-symbol">M</span><span class="eyebrow">MCII 节点共享</span><h1>每一个连接，<br>清晰可见。</h1><p>汇集来源，去重管理。<br>让节点状态与连接质量，一目了然。</p><div class="login-orbit" aria-hidden="true"><span></span><span></span><span></span><b><AppIcon name="nodes" /></b></div></div>
    <AuthPanel class="login-card" :registration="authOptions.registration" :notice="loginError" @authenticated="authenticatedFromPanel" />
  </main>
  <div v-else :class="['app-shell', { classic }]">
    <aside v-if="!classic" class="sidebar">
      <div class="brand"><span class="brand-symbol">M</span><div><strong>MCII节点共享</strong><span>MIHOMO 订阅共享</span></div></div>
      <button type="button" class="version-chip" aria-label="打开版本信息" title="版本信息" @click="versionsOpen = true"><span class="version-dot"></span><span class="version-label">版本</span><b>{{ system?.version || '读取中' }}</b><AppIcon name="right" /></button>
      <p class="nav-caption">工作空间</p>
      <nav aria-label="主要导航"><a v-for="item in sections" :key="item.id" :href="sectionHref(item.id)" :data-tour="`nav-${item.id}`" :title="item.label" :class="{ active: section === item.id }" :aria-current="section === item.id ? 'page' : undefined" @click="navigateLink($event, item.id)"><AppIcon :name="item.icon" /><span>{{ item.label }}</span><span v-if="item.id === 'sources' && ownSourceCount" class="nav-count">{{ ownSourceCount }}</span></a></nav>
    </aside>
    <nav v-if="!classic" class="mobile-tabbar" aria-label="手机导航"><a v-for="item in mobileTabs" :key="item.id" :href="sectionHref(item.id)" :data-tour="`nav-${item.id}`" :class="{ active: section === item.id }" :aria-current="section === item.id ? 'page' : undefined" @click="mobileGo($event, item.id)"><AppIcon :name="item.icon" /><span>{{ item.label.replace('我的', '') }}</span></a><button v-if="mobileOverflow.length" type="button" :class="{ active: moreActive }" data-tour="nav-more" @click="mobileMore = true"><AppIcon name="menu" /><span>更多</span></button><button v-if="isGuest" type="button" @click="openAuth('login')"><AppIcon name="login" /><span>登录</span></button></nav>
    <div class="workspace">
      <header v-if="classic" class="classic-head">
        <button type="button" class="icon-button classic-menu" aria-label="打开导航菜单" :aria-expanded="drawerOpen" @click="drawerOpen = true"><AppIcon name="menu" /></button>
        <a class="classic-brand" :href="sectionHref(homeSection)" @click="navigateLink($event, homeSection)"><span class="brand-symbol">M</span><strong>MCII节点共享</strong></a>
        <button type="button" class="classic-version" aria-label="打开版本信息" title="版本信息" @click="versionsOpen = true"><span class="version-dot"></span>{{ system?.version || '…' }}</button>
        <nav class="classic-tabs" aria-label="主要导航"><a v-for="item in sections" :key="item.id" :href="sectionHref(item.id)" :data-tour="`nav-${item.id}`" :title="item.label" :class="{ active: section === item.id }" :aria-current="section === item.id ? 'page' : undefined" @click="navigateLink($event, item.id)">{{ item.short }}<span v-if="item.id === 'sources' && ownSourceCount" class="nav-count">{{ ownSourceCount }}</span></a></nav>
        <div class="topbar-status"><span v-if="maintenanceOn" class="maintenance-pill" title="维护模式已开启"><AppIcon name="lock" />维护中</span><LayoutToggle /><ThemeSelect /><UserMenu v-if="authenticated" data-tour="user-menu" :busy="logoutBusy" @logout="logout" @tour="startTour" /><div v-else class="guest-actions"><button type="button" class="compact" @click="openAuth('login')"><AppIcon name="login" />登录</button><button v-if="authOptions.registration" type="button" class="primary compact" @click="openAuth('register')">注册</button></div></div>
      </header>
      <header v-else class="topbar"><span>工作空间 <span class="muted">/</span> <strong>{{ currentTitle }}</strong></span><div class="topbar-status"><span v-if="maintenanceOn" class="maintenance-pill" title="维护模式已开启"><AppIcon name="lock" />维护中</span><LayoutToggle /><ThemeSelect /><UserMenu v-if="authenticated" data-tour="user-menu" :busy="logoutBusy" @logout="logout" @tour="startTour" /><div v-else class="guest-actions"><button type="button" class="compact" @click="openAuth('login')"><AppIcon name="login" />登录</button><button v-if="authOptions.registration" type="button" class="primary compact" @click="openAuth('register')">注册</button></div></div></header>
      <main :key="viewKey" class="main-content">
        <div v-if="isGuest" class="guest-banner"><AppIcon name="info" /><div><strong>你正在以访客身份浏览</strong><p>可以查看节点、提供方与运行统计，服务器地址已隐藏。{{ authOptions.registration ? '注册或登录后' : '登录后' }}即可挑选节点、组合自己的订阅并导入客户端。</p></div><div class="guest-banner-actions"><button type="button" class="compact" @click="openAuth('login')">登录</button><button v-if="authOptions.registration" type="button" class="primary compact" @click="openAuth('register')">免费注册</button></div></div>
        <div v-if="maintenanceOn" class="maintenance-banner" role="status"><AppIcon name="lock" /><div><strong>{{ isAdmin ? '维护模式已开启' : '系统维护中' }}</strong><p>{{ system?.maintenance?.message || '管理员正在维护系统。' }}<template v-if="isAdmin">普通用户与用户管理员现在只能查看和拉取订阅，你仍可正常修改。</template><template v-else>期间可以查看内容和继续使用订阅，暂时不能修改。</template></p></div><a v-if="isAdmin && section !== 'settings'" :href="sectionHref('settings') + '#maintenance'" @click="navigateLink($event, 'settings')">前往设置</a></div>
        <div v-if="databaseUnavailable" class="notice error" role="alert">数据库暂不可用，节点、来源和设置可能无法读取。请恢复数据库连接后重试。<button @click="refreshOverview">重试连接</button></div>
        <div v-if="systemError && !databaseUnavailable" class="notice error" role="alert">{{ systemError }}<button @click="loadSystem">重试</button></div>
        <div v-if="system?.core?.state === 'error'" class="notice warning" role="alert">检测内核尚未就绪，节点检测可能失败。请检查服务运行状态。</div>
        <div v-if="reconnectError && !reconnecting" class="notice error" role="alert">{{ reconnectError }}<button @click="startReconnect">重新连接</button></div>
        <ClassicHome v-if="classic && visited.has('catalog')" v-show="section === 'catalog'" :paused="reconnecting || section !== 'catalog'" />
        <NodesView v-if="visited.has('nodes')" v-show="section === 'nodes'" :paused="reconnecting || section !== 'nodes'" />
        <SourcesView v-if="visited.has('sources')" v-show="section === 'sources'" :items="sources" :range="sourceScope" :loading="sourcesLoading" :error="sourcesError" @range="setSourceScope" @reload="loadSources" @updated="updateSource" @removed="removeSource" />
        <ProvidersView v-if="visited.has('providers')" v-show="section === 'providers'" :paused="reconnecting || section !== 'providers'" />
        <SubscriptionsView v-if="visited.has('subscriptions')" v-show="section === 'subscriptions'" :paused="reconnecting || section !== 'subscriptions'" />
        <RulesView v-if="visited.has('rules')" v-show="section === 'rules'" :paused="reconnecting || section !== 'rules'" />
        <AccountView v-if="visited.has('account')" v-show="section === 'account'" :paused="reconnecting || section !== 'account'" />
        <UsersView v-if="canManageUsers && visited.has('users')" v-show="section === 'users'" :paused="reconnecting || section !== 'users'" />
        <SettingsView v-if="isAdmin && visited.has('settings')" v-show="section === 'settings'" :system="system" @saved="settingsSaved" />
        <StatisticsView v-if="visited.has('statistics')" v-show="section === 'statistics'" :paused="reconnecting || section !== 'statistics'" />
      </main>
      <footer class="app-footer"><span>MCII节点共享</span><span>连接有序 · 状态可知</span></footer>
    </div>
    <VersionsDialog v-if="versionsOpen && isAdmin" :key="versionKey" :system="system" @close="versionsOpen = false" @restart="startReconnect" />
    <Modal v-else-if="versionsOpen" title="关于" @close="versionsOpen = false"><div class="about-version"><span class="brand-symbol">M</span><span class="eyebrow">MCII 节点共享</span><strong>{{ system?.version || '读取中…' }}</strong><p class="small muted">MCII节点共享 · 汇集来源、去重检测、组合订阅</p></div><div class="dialog-actions"><button type="button" class="primary" @click="versionsOpen = false">好的</button></div></Modal>
    <Modal v-if="authPrompt && isGuest" :title="authPrompt.mode === 'register' ? '注册账号' : '登录'" @close="authPrompt = null"><AuthPanel embedded :mode="authPrompt.mode" :reason="authPrompt.reason" :registration="authOptions.registration" :notice="loginError" @mode="authMode" @authenticated="authenticatedFromPanel" /></Modal>
    <Modal v-if="mobileMore" title="全部页面" @close="mobileMore = false"><div class="more-grid"><a v-for="item in sections" :key="item.id" :href="sectionHref(item.id)" :class="{ active: section === item.id }" @click="mobileGo($event, item.id)"><span class="more-icon"><AppIcon :name="item.icon" /></span><span>{{ item.label }}</span></a></div><button type="button" class="version-chip more-version" @click="mobileMore = false; versionsOpen = true"><span class="version-dot"></span><span class="version-label">版本</span><b>{{ system?.version || '读取中' }}</b><AppIcon name="right" /></button></Modal>
    <div v-if="classic && drawerOpen" class="classic-drawer-scrim" @click.self="drawerOpen = false"><nav class="classic-drawer" aria-label="导航菜单"><div class="classic-drawer-head"><span class="brand-symbol">M</span><strong>MCII节点共享</strong><button type="button" class="icon-button" aria-label="关闭导航菜单" @click="drawerOpen = false"><AppIcon name="close" /></button></div><a v-for="item in sections" :key="item.id" :href="sectionHref(item.id)" :class="{ active: section === item.id }" :aria-current="section === item.id ? 'page' : undefined" @click="drawerGo($event, item.id)"><AppIcon :name="item.icon" /><span>{{ item.label }}</span></a><button type="button" class="version-chip" @click="drawerOpen = false; versionsOpen = true"><span class="version-dot"></span><span class="version-label">版本</span><b>{{ system?.version || '读取中' }}</b><AppIcon name="right" /></button></nav></div>
    <Modal v-if="emailRequired && authenticated" title="绑定邮箱" locked><div class="email-required"><span class="email-required-icon"><AppIcon name="mail" /></span><p><strong>继续使用前，请先绑定并验证邮箱</strong></p><p class="small muted">邮箱用于登录、找回密码和接收重要通知。旧平台迁移或第三方注册的账号需要补齐一次，之后即可正常使用。</p></div><EmailBindForm submit-label="验证并继续" @bound="emailBound"><template #extra><button type="button" class="ghost" :disabled="logoutBusy" @click="logout">退出登录</button></template></EmailBindForm></Modal>
    <Modal v-if="passwordSetup && authenticated && !emailRequired" title="设置登录密码" locked><div class="email-required"><span class="email-required-icon"><AppIcon name="lock" /></span><p><strong>身份已验证，请设置登录密码</strong></p><p class="small muted">旧平台的密码没有迁移。设置后可以用用户名或邮箱加密码登录，也可以继续使用第三方账号登录。</p></div><PasswordSetupForm @done="passwordDone"><template #extra><button type="button" class="ghost" :disabled="logoutBusy" @click="logout">退出登录</button></template></PasswordSetupForm></Modal>
    <OnboardingTour v-if="tourOpen && authenticated && !emailRequired && !passwordSetup" :steps="tourSteps" @close="closeTour" />
    <div v-if="reconnecting" class="reconnect-overlay" role="status" aria-live="polite"><span class="spinner"></span><h2>正在重新连接服务</h2><p>服务重启后将自动恢复控制台。</p><p class="small muted">{{ reconnectError || '请稍候…' }}</p></div>
  </div>
</template>
