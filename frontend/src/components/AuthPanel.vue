<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { errorText, isCancelled, json, useApi } from '../api/system'
import { authOptions, type AuthResponse } from '../api/accounts'
import { emailSuffixAllowed, identityApi, oauthHref, useCountdown, type CodePurpose } from '../api/identity'
import { toast } from '../toast'
import AppIcon from './AppIcon.vue'
import EmailInput from './EmailInput.vue'
// 登录、注册、找回密码与“保持登录状态？”确认，登录页和访客弹窗共用
type Mode = 'login' | 'register' | 'reset'
const props = defineProps<{ mode?: 'login' | 'register'; registration: boolean; notice?: string; reason?: string; embedded?: boolean }>()
const emit = defineEmits<{ authenticated: [auth: AuthResponse]; mode: [value: 'login' | 'register'] }>()
const api = useApi()
const mode = ref<Mode>(props.mode === 'register' && props.registration ? 'register' : 'login')
watch(() => props.mode, value => { if (value) mode.value = value === 'register' && props.registration ? 'register' : 'login' })
watch(mode, value => { error.value = ''; if (value !== 'reset') emit('mode', value) })
const options = computed(() => authOptions.login)
const loginWith = ref<'password' | 'code'>('password')
const username = ref('')
const email = ref('')
const code = ref('')
const displayName = ref('')
const password = ref('')
const confirm = ref('')
const busy = ref(false)
const sending = ref(false)
const error = ref('')
const pending = ref<AuthResponse | null>(null)
const forget = ref(false)
const countdown = useCountdown()
const kmsiKey = 'nodepool-keep-signed-in'
const kmsiChoice = () => { try { return localStorage.getItem(kmsiKey) } catch { return null } }
const usernameValid = computed(() => /^[A-Za-z][A-Za-z0-9_-]{2,19}$/.test(username.value.trim()))
const emailValid = computed(() => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.value.trim()))
const suffixOk = computed(() => !emailValid.value || emailSuffixAllowed(email.value.trim(), options.value.email_suffixes))
const needEmail = computed(() => options.value.email)
const registerReady = computed(() => usernameValid.value && password.value.length >= 8 && password.value === confirm.value && (!needEmail.value || (emailValid.value && suffixOk.value && code.value.trim().length === 6)))
const thirdParty = computed(() => [options.value.github && { id: 'github', label: 'GitHub', icon: 'github' }, options.value.linuxdo && { id: 'linuxdo', label: 'LINUX DO', icon: 'linuxdo' }].filter(Boolean) as { id: string; label: string; icon: string }[])
const shownError = computed(() => error.value || props.notice || '')
const heading = computed(() => mode.value === 'login' ? '欢迎回来' : mode.value === 'register' ? '创建账号' : '找回密码')
const lead = computed(() => props.reason || (mode.value === 'login' ? '登录账号，管理来源与个人订阅。' : mode.value === 'register' ? '注册后即可挑选节点、组合自己的订阅。' : '通过已绑定的邮箱接收验证码，设置新密码。'))
function oauth(id: string) { return oauthHref(id, 'login', kmsiChoice() === 'yes') }
async function finish(auth: AuthResponse) {
  const choice = kmsiChoice()
  password.value = ''; confirm.value = ''; code.value = ''
  if (choice || !auth.authenticated) emit('authenticated', auth)
  else { pending.value = auth; forget.value = false }
}
async function sendCode(purpose: CodePurpose) {
  if (sending.value || countdown.left.value) return
  if (!emailValid.value) { error.value = '请输入正确的邮箱地址。'; return }
  if (purpose === 'register' && !suffixOk.value) { error.value = '暂不支持该邮箱后缀，请换一个常用邮箱。'; return }
  sending.value = true; error.value = ''
  try { const v = await identityApi.sendCode(email.value.trim(), purpose); countdown.start(v.cooldown || 60); toast('验证码已发送，请查收邮件', 'info') }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { sending.value = false }
}
async function login() {
  if (busy.value) return
  const byCode = loginWith.value === 'code'
  if (byCode ? !emailValid.value || code.value.trim().length !== 6 : !username.value.trim() || !password.value) return
  busy.value = true; error.value = ''
  try {
    const remember = kmsiChoice() === 'yes'
    await finish(byCode
      ? await api<AuthResponse>('/auth/login/code', json('POST', { email: email.value.trim(), code: code.value.trim(), remember }))
      : await api<AuthResponse>('/auth/login', json('POST', { username: username.value.trim(), password: password.value, remember })))
  } catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
async function register() {
  if (busy.value) return
  if (!usernameValid.value) { error.value = '用户名为 3–20 位，以字母开头，只能包含字母、数字、下划线或短横线。'; return }
  if (password.value.length < 8) { error.value = '密码至少 8 位。'; return }
  if (password.value !== confirm.value) { error.value = '两次输入的密码不一致。'; return }
  busy.value = true; error.value = ''
  try {
    const auth = await api<AuthResponse>('/auth/register', json('POST', { username: username.value.trim(), display_name: displayName.value.trim(), password: password.value, email: needEmail.value ? email.value.trim() : '', code: code.value.trim(), remember: kmsiChoice() === 'yes' }))
    toast('注册成功，欢迎加入')
    await finish(auth)
  } catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
async function reset() {
  if (busy.value) return
  if (password.value.length < 8 || password.value !== confirm.value) { error.value = '新密码至少 8 位，且两次输入一致。'; return }
  busy.value = true; error.value = ''
  try {
    await api('/auth/password/reset', json('POST', { email: email.value.trim(), code: code.value.trim(), password: password.value }))
    toast('密码已重置，请使用新密码登录')
    password.value = ''; confirm.value = ''; code.value = ''; username.value = email.value.trim(); loginWith.value = 'password'; mode.value = 'login'
  } catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
async function keep(remember: boolean) {
  const auth = pending.value
  if (!auth || busy.value) return
  busy.value = true
  try {
    if (forget.value) { try { localStorage.setItem(kmsiKey, remember ? 'yes' : 'no') } catch { /* 隐私模式下忽略 */ } }
    const result = remember ? await api<AuthResponse>('/auth/persist', json('POST', { remember: true })) : auth
    pending.value = null
    emit('authenticated', result)
    if (remember) toast('已保持登录 30 天，可在账号菜单中退出')
  } catch (cause) { if (!isCancelled(cause)) { pending.value = null; error.value = errorText(cause) } }
  finally { busy.value = false }
}
</script>

<template>
  <div v-if="pending" :class="['auth-panel', 'kmsi-card', { embedded }]" role="dialog" aria-labelledby="kmsi-title">
    <p class="eyebrow">MCII 节点共享</p><p class="kmsi-account"><AppIcon name="user" />{{ pending.user?.display_name || pending.username }}</p>
    <h2 id="kmsi-title">保持登录状态？</h2><p class="muted">这样可以减少要求你登录的次数。登录状态将保留 30 天，期间使用会自动续期。请勿在公用设备上选择“是”。</p>
    <label class="checkbox-label"><input v-model="forget" type="checkbox" />不再显示此消息</label>
    <div class="kmsi-actions"><button type="button" :disabled="busy" @click="keep(false)">否</button><button type="button" class="primary" :disabled="busy" @click="keep(true)"><span v-if="busy" class="spinner"></span>是</button></div>
  </div>
  <div v-else :class="['auth-panel', { embedded }]">
    <template v-if="!embedded"><p class="eyebrow">MCII 节点共享</p><h2>{{ heading }}</h2></template>
    <p class="muted">{{ lead }}</p>
    <div v-if="registration && mode !== 'reset'" class="scope-tabs auth-tabs" role="tablist" aria-label="登录或注册"><button type="button" role="tab" :aria-selected="mode === 'login'" :aria-pressed="mode === 'login'" @click="mode = 'login'">登录</button><button type="button" role="tab" :aria-selected="mode === 'register'" :aria-pressed="mode === 'register'" @click="mode = 'register'">注册</button></div>

    <form v-if="mode === 'login'" class="auth-form" @submit.prevent="login">
      <div v-if="options.email" class="auth-method" role="group" aria-label="登录方式"><button type="button" :class="{ active: loginWith === 'password' }" @click="loginWith = 'password'">密码登录</button><button type="button" :class="{ active: loginWith === 'code' }" @click="loginWith = 'code'">邮箱验证码</button></div>
      <template v-if="loginWith === 'password' || !options.email">
        <label class="form-field"><span class="field-label">{{ options.email ? '用户名或邮箱' : '用户名' }}</span><input v-model="username" autocomplete="username" required autofocus :disabled="busy" :placeholder="options.email ? '用户名或已绑定的邮箱' : '请输入用户名'" /></label>
        <label class="form-field"><span class="field-label field-label-row">密码<button v-if="options.email" type="button" class="text-button" @click="mode = 'reset'">忘记密码？</button></span><input v-model="password" type="password" autocomplete="current-password" required :disabled="busy" placeholder="请输入密码" /></label>
      </template>
      <template v-else>
        <label class="form-field"><span class="field-label">邮箱</span><EmailInput v-model="email" :disabled="busy" placeholder="已绑定的邮箱" /></label>
        <div class="form-field"><span class="field-label">验证码</span><div class="code-row"><input v-model="code" inputmode="numeric" maxlength="6" autocomplete="one-time-code" :disabled="busy" placeholder="6 位数字" aria-label="验证码" /><button type="button" :disabled="sending || countdown.left.value > 0 || !emailValid" @click="sendCode('login')"><span v-if="sending" class="spinner"></span>{{ countdown.left.value ? `${countdown.left.value} 秒后重发` : '获取验证码' }}</button></div></div>
      </template>
      <p v-if="shownError" class="notice error" role="alert">{{ shownError }}</p>
      <button class="primary full" :disabled="busy || (loginWith === 'code' && options.email ? !emailValid || code.trim().length !== 6 : !username.trim() || !password)" type="submit"><span v-if="busy" class="spinner"></span>{{ busy ? '正在登录' : '登录' }}<AppIcon v-if="!busy" name="right" /></button>
      <template v-if="thirdParty.length"><div class="auth-divider"><span>或使用第三方账号</span></div><div class="oauth-row"><a v-for="p in thirdParty" :key="p.id" :class="['oauth-button', p.id]" :href="oauth(p.id)"><AppIcon :name="p.icon" />{{ p.label }}</a></div></template>
      <p class="small muted auth-switch">{{ registration ? '还没有账号？' : '账号由管理员创建' }}<button v-if="registration" type="button" class="text-button" @click="mode = 'register'">立即注册</button></p>
    </form>

    <form v-else-if="mode === 'register'" class="auth-form" @submit.prevent="register">
      <template v-if="needEmail">
        <label class="form-field"><span class="field-label">邮箱</span><EmailInput v-model="email" autofocus :disabled="busy" placeholder="用于登录和找回密码" /><span v-if="!suffixOk" class="field-hint danger-text">暂不支持该邮箱后缀，请换一个常用邮箱。</span></label>
        <div class="form-field"><span class="field-label">邮箱验证码</span><div class="code-row"><input v-model="code" inputmode="numeric" maxlength="6" autocomplete="one-time-code" :disabled="busy" placeholder="6 位数字" aria-label="邮箱验证码" /><button type="button" :disabled="sending || countdown.left.value > 0 || !emailValid || !suffixOk" @click="sendCode('register')"><span v-if="sending" class="spinner"></span>{{ countdown.left.value ? `${countdown.left.value} 秒后重发` : '获取验证码' }}</button></div></div>
      </template>
      <div class="auth-grid">
        <label class="form-field"><span class="field-label">用户名</span><input v-model="username" autocomplete="username" required :autofocus="!needEmail" :disabled="busy" maxlength="20" placeholder="字母开头，3–20 位" /><span v-if="username && !usernameValid" class="field-hint danger-text">字母开头，仅限字母、数字、_ 或 -，注册后不可修改。</span><span v-else class="field-hint">注册后不可修改</span></label>
        <label class="form-field"><span class="field-label">昵称 <small class="muted">可选</small></span><input v-model="displayName" autocomplete="nickname" :disabled="busy" maxlength="40" placeholder="别人看到的名字" /></label>
      </div>
      <div class="auth-grid">
        <label class="form-field"><span class="field-label">密码</span><input v-model="password" type="password" autocomplete="new-password" required :disabled="busy" placeholder="至少 8 位" /></label>
        <label class="form-field"><span class="field-label">确认密码</span><input v-model="confirm" type="password" autocomplete="new-password" required :disabled="busy" placeholder="再输入一次" /></label>
      </div>
      <p v-if="shownError" class="notice error" role="alert">{{ shownError }}</p>
      <button class="primary full" :disabled="busy || !registerReady" type="submit"><span v-if="busy" class="spinner"></span>{{ busy ? '正在注册' : '注册并登录' }}<AppIcon v-if="!busy" name="right" /></button>
      <template v-if="thirdParty.length"><div class="auth-divider"><span>或直接使用第三方账号注册</span></div><div class="oauth-row"><a v-for="p in thirdParty" :key="p.id" :class="['oauth-button', p.id]" :href="oauth(p.id)"><AppIcon :name="p.icon" />{{ p.label }}</a></div><p v-if="needEmail" class="small muted oauth-note">第三方注册后仍需绑定并验证邮箱。</p></template>
      <p class="small muted auth-switch">已有账号？<button type="button" class="text-button" @click="mode = 'login'">去登录</button></p>
    </form>

    <form v-else class="auth-form" @submit.prevent="reset">
      <label class="form-field"><span class="field-label">已绑定的邮箱</span><EmailInput v-model="email" autofocus :disabled="busy" placeholder="name@qq.com" /></label>
      <div class="form-field"><span class="field-label">验证码</span><div class="code-row"><input v-model="code" inputmode="numeric" maxlength="6" autocomplete="one-time-code" :disabled="busy" placeholder="6 位数字" aria-label="验证码" /><button type="button" :disabled="sending || countdown.left.value > 0 || !emailValid" @click="sendCode('reset')"><span v-if="sending" class="spinner"></span>{{ countdown.left.value ? `${countdown.left.value} 秒后重发` : '获取验证码' }}</button></div></div>
      <div class="auth-grid">
        <label class="form-field"><span class="field-label">新密码</span><input v-model="password" type="password" autocomplete="new-password" required :disabled="busy" placeholder="至少 8 位" /></label>
        <label class="form-field"><span class="field-label">确认新密码</span><input v-model="confirm" type="password" autocomplete="new-password" required :disabled="busy" placeholder="再输入一次" /></label>
      </div>
      <p v-if="shownError" class="notice error" role="alert">{{ shownError }}</p>
      <button class="primary full" :disabled="busy || !emailValid || code.trim().length !== 6 || password.length < 8" type="submit"><span v-if="busy" class="spinner"></span>重置密码</button>
      <p class="small muted auth-switch">想起来了？<button type="button" class="text-button" @click="mode = 'login'">返回登录</button></p>
    </form>
  </div>
</template>
