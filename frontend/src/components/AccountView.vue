<script setup lang="ts">
import { ref, watch } from 'vue'
import { errorText, isCancelled, json, useApi } from '../api/system'
import { absoluteURL, copyText, currentUser, loadProfile, profile } from '../api/accounts'
import { useAccountScope } from '../community'
import AppIcon from './AppIcon.vue'
import LeaseCheckin from './LeaseCheckin.vue'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const displayName = ref(currentUser.value?.display_name ?? '')
const password = ref('')
const saving = ref(false)
const error = ref('')
const message = ref('')
async function load() {
  if (props.paused) return
  const ticket = scope.ticket()
  try { await loadProfile(); if (scope.current(ticket)) { displayName.value = currentUser.value?.display_name ?? ''; error.value = '' } }
  catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
}
watch(() => props.paused, paused => { if (!paused) void load(); else password.value = '' }, { immediate: true })
async function save() {
  if (saving.value || props.paused) return
  const ticket = scope.ticket()
  saving.value = true; error.value = ''; message.value = ''
  try {
    await api('/me', json('PATCH', { display_name: displayName.value.trim(), ...(password.value ? { password: password.value } : {}) }))
    if (!scope.current(ticket)) return
    password.value = ''; message.value = '账号设置已保存。'; await load()
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { saving.value = false }
}
async function copy() {
  try { await copyText(absoluteURL(profile.value?.provider_path || `/nodes?provider_id=${currentUser.value?.id}`)); message.value = '提供方入口已复制。' }
  catch (cause) { error.value = errorText(cause) }
}
</script>
<template>
  <section>
    <div class="page-heading"><div><h1>我的账号</h1><p class="muted">{{ currentUser?.username }}</p></div></div>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p><p v-if="message" class="notice success" role="status">{{ message }}</p>
    <LeaseCheckin :paused="paused" />
    <div class="account-layout">
      <div>
        <section class="panel">
          <h2>订阅额度</h2>
          <div class="usage-grid"><div><span>节点</span><strong>{{ profile?.usage.nodes ?? 0 }} <small>/ {{ currentUser?.node_limit ?? 200 }}</small></strong></div><div><span>来源</span><strong>{{ profile?.usage.sources ?? 0 }} <small>/ {{ currentUser?.source_limit ?? 3 }}</small></strong></div><div><span>订阅</span><strong>{{ profile?.usage.subscriptions ?? 0 }} <small>/ {{ currentUser?.subscription_limit ?? 1 }}</small></strong></div></div>
          <p class="small muted">额度按账号全部组合订阅合计，由管理员调整。删除某来源的最后引用将释放其有效席位。</p>
        </section>
        <section class="panel">
          <h2>我的提供方入口</h2><p class="small muted">分享这个入口，其他用户登录后可查看和领取你提供的节点。</p>
          <div class="copy-field"><input aria-label="我的提供方入口" :value="absoluteURL(profile?.provider_path || `/nodes?provider_id=${currentUser?.id}`)" readonly /><button type="button" class="icon-button" aria-label="复制提供方入口" title="复制提供方入口" @click="copy"><AppIcon name="copy" /></button></div>
        </section>
      </div>
      <form class="panel" @submit.prevent="save">
        <h2>账号设置</h2>
        <fieldset :disabled="saving || paused">
          <label class="form-field"><span class="field-label">显示名称</span><input v-model="displayName" maxlength="80" required /></label>
          <label class="form-field"><span class="field-label">新密码 <small class="muted">可选</small></span><input v-model="password" type="password" minlength="8" maxlength="1024" autocomplete="new-password" /><span class="field-hint">至少 8 位；留空保留当前密码。</span></label>
        </fieldset>
        <div class="dialog-actions"><button class="primary" :disabled="saving || paused">{{ saving ? '保存中…' : '保存账号' }}</button></div>
      </form>
    </div>
  </section>
</template>
