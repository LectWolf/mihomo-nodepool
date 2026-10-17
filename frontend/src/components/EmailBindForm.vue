<script setup lang="ts">
import { computed, ref } from 'vue'
import { errorText, isCancelled } from '../api/system'
import { authOptions, currentUser, type User } from '../api/accounts'
import { emailSuffixAllowed, identityApi, useCountdown } from '../api/identity'
import { toast } from '../toast'
import EmailInput from './EmailInput.vue'
// 绑定或换绑邮箱：向新邮箱发送验证码，验证通过后生效
const props = defineProps<{ current?: string; submitLabel?: string }>()
const emit = defineEmits<{ bound: [user: User]; cancel: [] }>()
const email = ref('')
const code = ref('')
const busy = ref(false)
const sending = ref(false)
const error = ref('')
const countdown = useCountdown()
const valid = computed(() => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.value.trim()))
const suffixOk = computed(() => !valid.value || emailSuffixAllowed(email.value.trim(), authOptions.login.email_suffixes))
async function send() {
  if (!valid.value || !suffixOk.value || sending.value || countdown.left.value) return
  sending.value = true; error.value = ''
  try { const v = await identityApi.sendBindCode(email.value.trim()); countdown.start(v.cooldown || 60); toast('验证码已发送到新邮箱', 'info') }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { sending.value = false }
}
async function submit() {
  if (busy.value || code.value.trim().length !== 6) return
  busy.value = true; error.value = ''
  try {
    const user = await identityApi.bindEmail(email.value.trim(), code.value.trim())
    currentUser.value = user
    toast(props.current ? '邮箱已更换' : '邮箱已绑定')
    email.value = ''; code.value = ''
    emit('bound', user)
  } catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
</script>

<template>
  <form class="email-bind" @submit.prevent="submit">
    <label class="form-field"><span class="field-label">{{ current ? '新邮箱' : '邮箱' }}</span><EmailInput v-model="email" :disabled="busy" placeholder="输入邮箱，例如 name@qq.com" /><span v-if="!suffixOk" class="field-hint danger-text">暂不支持该邮箱后缀，请换一个常用邮箱。</span></label>
    <div class="form-field"><span class="field-label">验证码</span><div class="code-row"><input v-model="code" inputmode="numeric" maxlength="6" autocomplete="one-time-code" :disabled="busy" placeholder="6 位数字" aria-label="邮箱验证码" /><button type="button" :disabled="sending || countdown.left.value > 0 || !valid || !suffixOk" @click="send"><span v-if="sending" class="spinner"></span>{{ countdown.left.value ? `${countdown.left.value} 秒后重发` : '发送验证码' }}</button></div></div>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <div class="email-bind-actions"><slot name="extra" /><button type="submit" class="primary" :disabled="busy || code.trim().length !== 6 || !valid"><span v-if="busy" class="spinner"></span>{{ submitLabel || (current ? '确认更换' : '绑定邮箱') }}</button></div>
  </form>
</template>
