<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { abortRequests, apiEvents, errorText, isCancelled, json, useApi, type Source, type SystemInfo } from './api/system'
import { clearAccount, currentUser, isAdmin, loadProfile, type AuthResponse } from './api/accounts'
import { usePolling } from './polling'
import NodesView from './components/NodesView.vue'
import SourcesView from './components/SourcesView.vue'
import SettingsView from './components/SettingsView.vue'
import ProvidersView from './components/ProvidersView.vue'
import SubscriptionsView from './components/SubscriptionsView.vue'
import UsersView from './components/UsersView.vue'
import AccountView from './components/AccountView.vue'
import VersionsDialog from './components/VersionsDialog.vue'
import Modal from './components/Modal.vue'
import AppIcon from './components/AppIcon.vue'
import ThemeSelect from './components/ThemeSelect.vue'
import { navigate, route, sectionHref, type Section } from './router'
const api = useApi()
const authenticated = ref(false)
const booting = ref(true)
const username = ref('admin')
const password = ref('')
const loginBusy = ref(false)
const loginError = ref('')
const logoutBusy = ref(false)
const section = computed(() => route.value.section)
const visited = reactive(new Set<Section>([section.value]))
const allSections = [
  { id: 'nodes', label: '节点总览', icon: 'nodes', admin: false },
  { id: 'sources', label: '来源管理', icon: 'sources', admin: false },
  { id: 'providers', label: '提供方', icon: 'users', admin: false },
  { id: 'subscriptions', label: '我的订阅', icon: 'link', admin: false },
  { id: 'account', label: '我的账号', icon: 'user', admin: false },
  { id: 'users', label: '用户管理', icon: 'users', admin: true },
  { id: 'settings', label: '运行设置', icon: 'settings', admin: true },
] as const
const sections = computed(() => allSections.filter(item => !item.admin || isAdmin.value))
const currentTitle = computed(() => sections.value.find(item => item.id === section.value)?.label)
const displayName = computed(() => currentUser.value?.display_name || currentUser.value?.username || '')
watch([section, authenticated, isAdmin], ([id, loggedIn, admin]) => {
  if (loggedIn && !admin && (id === 'users' || id === 'settings')) { navigate('nodes'); return }
  visited.add(id)
}, { flush: 'sync' })
function navigateLink(event: MouseEvent, id: Section) {
  if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return
  event.preventDefault(); navigate(id)
}
const system = ref<SystemInfo | null>(null)
const systemError = ref('')
const sources = ref<Source[]>([])
const sourcesLoading = ref(false)
const sourcesError = ref('')
const databaseUnavailable = ref(false)
const versionsOpen = ref(false)
const versionKey = ref(0)
const reconnecting = ref(false)
const reconnectError = ref('')
let restartAt = 0
let systemLoading = false
let connecting = false
let sourcesRevision = 0
let sessionRevision = 0
function updateSource(source: Source) { sourcesRevision++; sources.value = sources.value.map(item => item.id === source.id ? source : item) }
function removeSource(id: number) { sourcesRevision++; sources.value = sources.value.filter(item => item.id !== id) }
function clearSession() {
  sessionRevision++; abortRequests(); authenticated.value = false; clearAccount()
  system.value = null; sources.value = []; sourcesError.value = ''; systemError.value = ''
  versionsOpen.value = false; password.value = ''; reconnecting.value = false; reconnectError.value = ''
  databaseUnavailable.value = false; sourcesRevision++; visited.clear(); visited.add(section.value)
}
function unauthorized() {
  if (authenticated.value) loginError.value = '登录已过期或账号已停用，请重新登录。'
  clearSession()
}
function unavailable() { databaseUnavailable.value = true }
async function loadSystem() {
  if (!authenticated.value || systemLoading) return
  systemLoading = true
  const revision = sessionRevision
  try {
    const value = await api<SystemInfo>('/system')
    if (!authenticated.value || revision !== sessionRevision) return
    system.value = value
    if (value.database) databaseUnavailable.value = !value.database.connected
    systemError.value = ''
  } catch (error) { if (!isCancelled(error) && authenticated.value && revision === sessionRevision) systemError.value = errorText(error) }
  finally { systemLoading = false }
}
async function loadSources() {
  if (!authenticated.value || sourcesLoading.value) return
  sourcesLoading.value = true
  const revision = sourcesRevision
  try {
    const value = await api<{ items: Source[] }>('/sources')
    if (authenticated.value && revision === sourcesRevision) { sources.value = value.items ?? []; sourcesError.value = ''; databaseUnavailable.value = false }
  } catch (error) { if (!isCancelled(error) && authenticated.value && revision === sourcesRevision) sourcesError.value = errorText(error) }
  finally {
    sourcesLoading.value = false
    if (authenticated.value && revision !== sourcesRevision) void loadSources()
  }
}
async function refreshOverview() { await Promise.all([loadSystem(), loadSources()]) }
async function establishSession(auth: AuthResponse) {
  if (!auth.authenticated || !auth.user) { clearSession(); return }
  currentUser.value = auth.user; authenticated.value = true
  await Promise.all([refreshOverview(), loadProfile().catch(error => { if (!isCancelled(error) && authenticated.value) systemError.value = errorText(error) })])
}
async function login() {
  if (!username.value.trim() || !password.value || loginBusy.value) return
  loginBusy.value = true; loginError.value = ''
  try {
    const auth = await api<AuthResponse>('/auth/login', json('POST', { username: username.value.trim(), password: password.value }))
    password.value = ''; await establishSession(auth)
  } catch (error) { if (!isCancelled(error)) loginError.value = errorText(error) }
  finally { loginBusy.value = false }
}
async function logout() {
  if (logoutBusy.value) return
  logoutBusy.value = true
  try { await api('/auth/logout', json('POST')); clearSession() }
  catch (error) { if (!isCancelled(error)) systemError.value = errorText(error) }
  finally { logoutBusy.value = false }
}
function startReconnect() { versionsOpen.value = false; restartAt = Date.now(); reconnecting.value = true; reconnectError.value = '' }
async function reconnect() {
  if (connecting || Date.now() - restartAt < 3000) return
  if (Date.now() - restartAt > 120000) { reconnecting.value = false; reconnectError.value = '服务尚未恢复，请确认服务已启动，然后重新连接。'; return }
  connecting = true
  try {
    const auth = await api<AuthResponse>('/auth')
    if (!auth.authenticated) { clearSession(); loginError.value = '服务已恢复，请重新登录。'; return }
    reconnecting.value = false; reconnectError.value = ''; versionKey.value++
    await establishSession(auth)
  } catch (error) { if (!isCancelled(error)) reconnectError.value = '正在等待服务恢复，稍后会自动重连…' }
  finally { connecting = false }
}
usePolling(async () => {
  if (reconnecting.value) await reconnect()
  else if (authenticated.value && !reconnectError.value) await refreshOverview()
}, 4000, false)
onMounted(async () => {
  apiEvents.addEventListener('unauthorized', unauthorized)
  apiEvents.addEventListener('database-unavailable', unavailable)
  try { await establishSession(await api<AuthResponse>('/auth')) }
  catch (error) { if (!isCancelled(error)) loginError.value = errorText(error) }
  finally { booting.value = false }
})
onBeforeUnmount(() => {
  apiEvents.removeEventListener('unauthorized', unauthorized)
  apiEvents.removeEventListener('database-unavailable', unavailable)
})
</script>

<template>
  <ThemeSelect v-if="!authenticated" class="guest-theme" />
  <div v-if="booting" class="boot-screen" role="status"><span class="brand-symbol">N</span><span class="spinner"></span>正在连接 NodePool…</div>
  <main v-else-if="!authenticated" class="login-screen">
    <div class="login-intro"><span class="brand-symbol">N</span><span class="eyebrow">MIHOMO NODEPOOL</span><h1>每一个连接，<br>清晰可见。</h1><p>汇集来源，去重管理。<br>让节点状态与连接质量，一目了然。</p><div class="login-orbit" aria-hidden="true"><span></span><span></span><span></span><b><AppIcon name="nodes" /></b></div></div>
    <form class="login-card" @submit.prevent="login">
      <p class="eyebrow">NODEPOOL</p><h2>欢迎回来</h2><p class="muted">登录账号，管理来源与个人订阅。</p>
      <label class="form-field"><span class="field-label">用户名</span><input v-model="username" autocomplete="username" required autofocus :disabled="loginBusy" placeholder="请输入用户名" /></label>
      <label class="form-field"><span class="field-label">密码</span><input v-model="password" type="password" autocomplete="current-password" required :disabled="loginBusy" placeholder="请输入密码" /></label>
      <p v-if="loginError" class="notice error" role="alert">{{ loginError }}</p>
      <button class="primary full" :disabled="loginBusy || !username.trim() || !password" type="submit"><span v-if="loginBusy" class="spinner"></span>{{ loginBusy ? '正在登录' : '登录控制台' }}<AppIcon v-if="!loginBusy" name="right" /></button>
      <p class="small muted">账号由管理员创建</p>
    </form>
  </main>
  <div v-else class="app-shell">
    <aside class="sidebar">
      <div class="brand"><span class="brand-symbol">N</span><div><strong>NodePool</strong><span>MIHOMO 控制台</span></div></div>
      <button class="version-text text-button" aria-label="打开版本信息" @click="versionsOpen = true">{{ system?.version || '版本信息' }}</button>
      <p class="nav-caption">工作空间</p>
      <nav aria-label="主要导航"><a v-for="item in sections" :key="item.id" :href="sectionHref(item.id)" :class="{ active: section === item.id }" :aria-current="section === item.id ? 'page' : undefined" @click="navigateLink($event, item.id)"><AppIcon :name="item.icon" /><span>{{ item.label }}</span><span v-if="item.id === 'sources' && sources.length" class="nav-count">{{ sources.length }}</span></a></nav>
      <div class="sidebar-bottom"><button class="logout" :disabled="logoutBusy" @click="logout"><AppIcon name="logout" />{{ logoutBusy ? '正在退出…' : '退出登录' }}</button></div>
    </aside>
    <div class="workspace">
      <header class="topbar"><span>工作空间 <span class="muted">/</span> <strong>{{ currentTitle }}</strong></span><div class="topbar-status"><ThemeSelect /><span class="admin-avatar">{{ displayName.slice(0, 1).toUpperCase() }}</span><span class="admin-name">{{ displayName }}</span><button class="mobile-logout text-button" :disabled="logoutBusy" @click="logout">退出</button></div></header>
      <main class="main-content">
        <div v-if="databaseUnavailable" class="notice error" role="alert">数据库暂不可用，节点、来源和设置可能无法读取。请恢复数据库连接后重试。<button @click="refreshOverview">重试连接</button></div>
        <div v-if="systemError && !databaseUnavailable" class="notice error" role="alert">{{ systemError }}<button @click="loadSystem">重试</button></div>
        <div v-if="system?.core?.state === 'error'" class="notice warning" role="alert">检测内核尚未就绪，节点检测可能失败。请检查服务运行状态。</div>
        <div v-if="reconnectError && !reconnecting" class="notice error" role="alert">{{ reconnectError }}<button @click="startReconnect">重新连接</button></div>
        <NodesView v-if="visited.has('nodes')" v-show="section === 'nodes'" :paused="reconnecting || section !== 'nodes'" />
        <SourcesView v-if="visited.has('sources')" v-show="section === 'sources'" :items="sources" :loading="sourcesLoading" :error="sourcesError" @reload="loadSources" @updated="updateSource" @removed="removeSource" />
        <ProvidersView v-if="visited.has('providers')" v-show="section === 'providers'" :paused="reconnecting || section !== 'providers'" />
        <SubscriptionsView v-if="visited.has('subscriptions')" v-show="section === 'subscriptions'" :paused="reconnecting || section !== 'subscriptions'" />
        <AccountView v-if="visited.has('account')" v-show="section === 'account'" :paused="reconnecting || section !== 'account'" />
        <UsersView v-if="isAdmin && visited.has('users')" v-show="section === 'users'" :paused="reconnecting || section !== 'users'" />
        <SettingsView v-if="isAdmin && visited.has('settings')" v-show="section === 'settings'" :system="system" />
      </main>
      <footer class="app-footer"><span>Mihomo NodePool</span><span>连接有序 · 状态可知</span></footer>
    </div>
    <VersionsDialog v-if="versionsOpen && isAdmin" :key="versionKey" @close="versionsOpen = false" @restart="startReconnect" />
    <Modal v-else-if="versionsOpen" title="版本信息" @close="versionsOpen = false"><p>Mihomo NodePool <strong>{{ system?.version || '读取中…' }}</strong></p></Modal>
    <div v-if="reconnecting" class="reconnect-overlay" role="status" aria-live="polite"><span class="spinner"></span><h2>正在重新连接服务</h2><p>服务重启后将自动恢复控制台。</p><p class="small muted">{{ reconnectError || '请稍候…' }}</p></div>
  </div>
</template>
