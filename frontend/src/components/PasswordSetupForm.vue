<script setup lang="ts">
import { ref } from 'vue'
import { errorText, isCancelled, json, useApi } from '../api/system'
import { currentUser, type User } from '../api/accounts'
import { toast } from '../toast'
// 没有密码的账号验证身份后补设登录密码
const emit = defineEmits<{ done: [user: User] }>()
const api = useApi()
const password = ref('')
const confirm = ref('')
const busy = ref(false)
const error = ref('')
async function submit() {
  if (busy.value) return
  if (password.value.length < 8) { error.value = '密码至少 8 位。'; return }
  if (password.value !== confirm.value) { error.value = '两次输入的密码不一致。'; return }
  busy.value = true; error.value = ''
  try {
    const user = await api<User>('/me', json('PATCH', { password: password.value }))
    currentUser.value = user
    password.value = ''; confirm.value = ''
    toast('登录密码已设置')
    emit('done', user)
  } catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
</script>

<template>
  <form class="email-bind" @submit.prevent="submit">
    <input class="sr-only" :value="currentUser?.username" autocomplete="username" readonly tabindex="-1" aria-hidden="true" />
    <label class="form-field"><span class="field-label">新密码</span><input v-model="password" type="password" minlength="8" maxlength="1024" autocomplete="new-password" autofocus :disabled="busy" placeholder="至少 8 位" /></label>
    <label class="form-field"><span class="field-label">确认新密码</span><input v-model="confirm" type="password" minlength="8" maxlength="1024" autocomplete="new-password" :disabled="busy" placeholder="再输入一次" /></label>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p>
    <div class="email-bind-actions"><slot name="extra" /><button type="submit" class="primary" :disabled="busy || password.length < 8 || !confirm"><span v-if="busy" class="spinner"></span>设置并继续</button></div>
  </form>
</template>
