<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi } from '../api/system'
import { currentUser, loadProfile, type Page, type User } from '../api/accounts'
import { levelForXP, levelLabel, userCommunity, useAccountScope } from '../community'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
const props = defineProps<{ paused: boolean }>()
const api = useApi()
const scope = useAccountScope(() => props.paused)
const data = ref<Page<User> | null>(null)
const q = ref('')
const page = ref(1)
const loading = ref(false)
const error = ref('')
const message = ref('')
const open = ref(false)
const editing = ref<User | null>(null)
const saving = ref(false)
const formError = ref('')
const form = reactive({ username: '', display_name: '', password: '', enabled: true, node_limit: 200, source_limit: 3, subscription_limit: 1, xp: 0, points: 0 })
let timer: ReturnType<typeof setTimeout> | undefined
let sequence = 0
let controller: AbortController | undefined
async function load() {
  if (props.paused) return
  const current = ++sequence, ticket = scope.ticket()
  controller?.abort(); controller = new AbortController(); loading.value = true
  try {
    const result = await api<Page<User>>(`/users?${new URLSearchParams({ q: q.value, page: String(page.value), page_size: '20' })}`, { signal: controller.signal })
    if (current === sequence && scope.current(ticket)) { data.value = result; error.value = '' }
  } catch (cause) { if (current === sequence && scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { if (current === sequence) loading.value = false }
}
watch(q, () => { page.value = 1 })
watch([q, page, () => props.paused], () => {
  clearTimeout(timer); controller?.abort(); ++sequence
  if (!props.paused) timer = setTimeout(() => void load(), 250)
  else { open.value = false; form.password = ''; loading.value = false }
}, { immediate: true })
onBeforeUnmount(() => { clearTimeout(timer); controller?.abort(); ++sequence })
function edit(user: User | null) {
  const community = userCommunity(user)
  editing.value = user
  Object.assign(form, { username: user?.username ?? '', display_name: user?.display_name ?? '', password: '', enabled: user?.enabled ?? true, node_limit: user?.node_limit ?? 200, source_limit: user?.source_limit ?? 3, subscription_limit: user?.subscription_limit ?? 1, xp: community.xp, points: community.points })
  formError.value = ''; open.value = true
}
const valid = computed(() => !!form.username.trim() && (!!editing.value || form.password.length >= 8) && [form.node_limit, form.source_limit, form.subscription_limit].every(n => Number.isInteger(n) && n > 0) && [form.xp, form.points].every(n => Number.isSafeInteger(n) && n >= 0))
const resultingLevel = computed(() => levelLabel(levelForXP(form.xp)))
async function save() {
  if (!valid.value || saving.value || props.paused) return
  const ticket = scope.ticket(), target = editing.value
  saving.value = true; formError.value = ''
  try {
    const body = { display_name: form.display_name.trim(), node_limit: form.node_limit, source_limit: form.source_limit, subscription_limit: form.subscription_limit, ...(form.password ? { password: form.password } : {}) }
    if (target) await api(`/users/${target.id}`, json('PATCH', { ...body, enabled: form.enabled, xp: form.xp, points: form.points }))
    else await api('/users', json('POST', { ...body, username: form.username.trim() }))
    if (!scope.current(ticket)) return
    open.value = false; form.password = ''; message.value = target ? '用户设置、经验与积分已保存。' : '用户已创建，可使用用户名和密码登录。'
    await load()
    if (target?.id === currentUser.value?.id && scope.current(ticket)) await loadProfile()
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) formError.value = errorText(cause) }
  finally { saving.value = false }
}
</script>
<template>
  <section>
    <div class="page-heading"><div><h1>用户管理</h1><p class="muted">管理账号、访问状态、等级积分与订阅额度。</p></div><button class="primary" :disabled="paused" @click="edit(null)"><AppIcon name="plus" />创建用户</button></div>
    <p v-if="message" class="notice success" role="status">{{ message }}</p><p v-if="error" class="notice error" role="alert">{{ error }}<button @click="load">重试</button></p>
    <div class="panel">
      <div class="panel-heading"><h2>用户 <span class="count">{{ data?.total ?? 0 }}</span></h2><label class="search-field"><span class="sr-only">搜索用户</span><AppIcon name="search" /><input v-model="q" type="search" placeholder="搜索用户名或昵称" /></label></div>
      <div class="table-wrap" :aria-busy="loading"><table class="users-table"><thead><tr><th>用户</th><th>等级 / 积分</th><th>节点额度</th><th>来源额度</th><th>订阅额度</th><th>状态</th><th>创建时间</th><th class="right">操作</th></tr></thead><tbody>
        <tr v-for="user in data?.items ?? []" :key="user.id"><td><strong>{{ user.display_name || user.username }}</strong><span class="cell-sub">{{ user.username }} · {{ user.role === 'admin' ? '管理员' : '用户' }}</span></td><td><strong>{{ levelLabel(userCommunity(user).level) }}</strong><span class="cell-sub">{{ userCommunity(user).xp.toLocaleString() }} XP · {{ userCommunity(user).points.toLocaleString() }} 积分</span></td><td>{{ user.node_limit }}</td><td>{{ user.source_limit }}</td><td>{{ user.subscription_limit }}</td><td><span class="badge" :class="user.enabled ? 'ready' : 'error'">{{ user.enabled ? '启用' : '停用' }}</span></td><td>{{ formatDate(user.created_at) }}</td><td class="right"><button class="compact" @click="edit(user)"><AppIcon name="edit" />编辑</button></td></tr>
      </tbody></table></div>
      <p v-if="!loading && !data?.items.length" class="small muted">暂无匹配用户</p>
      <div class="pagination"><span class="small muted">{{ loading ? '正在读取…' : `共 ${data?.total ?? 0} 个用户` }}</span><div><button aria-label="上一页用户" :disabled="page === 1 || loading" @click="page--"><AppIcon name="left" /></button><span>{{ page }}</span><button aria-label="下一页用户" :disabled="loading || page * 20 >= (data?.total ?? 0)" @click="page++"><AppIcon name="right" /></button></div></div>
    </div>
    <Modal v-if="open" :title="editing ? '编辑用户' : '创建用户'" :busy="saving" @close="open = false">
      <form @submit.prevent="save"><fieldset :disabled="saving || paused">
        <label class="form-field"><span class="field-label">用户名</span><input v-model="form.username" required maxlength="80" :disabled="!!editing" autocomplete="off" /></label>
        <label class="form-field"><span class="field-label">显示名称 <small class="muted">可选</small></span><input v-model="form.display_name" maxlength="80" /></label>
        <label class="form-field"><span class="field-label">{{ editing ? '重置密码' : '密码' }} <small v-if="editing" class="muted">可选</small></span><input v-model="form.password" type="password" :required="!editing" minlength="8" maxlength="1024" autocomplete="new-password" /><span class="field-hint">{{ editing ? '留空保留当前密码；重置后原会话失效。' : '至少 8 位，请通过可信方式交给用户。' }}</span></label>
        <div v-if="editing" class="form-grid"><label class="form-field"><span class="field-label">经验 XP</span><input v-model.number="form.xp" type="number" min="0" :max="Number.MAX_SAFE_INTEGER" step="1" required /><span class="field-hint">等级由经验计算：{{ resultingLevel }}</span></label><label class="form-field"><span class="field-label">积分</span><input v-model.number="form.points" type="number" min="0" :max="Number.MAX_SAFE_INTEGER" step="1" required /></label></div>
        <div class="form-grid quota-fields"><label class="form-field"><span class="field-label">节点上限</span><input v-model.number="form.node_limit" required type="number" min="1" max="100000" /></label><label class="form-field"><span class="field-label">来源上限</span><input v-model.number="form.source_limit" required type="number" min="1" max="10000" /></label><label class="form-field"><span class="field-label">订阅上限</span><input v-model.number="form.subscription_limit" required type="number" min="1" max="1000" /></label></div>
        <p class="field-hint">额度按账号全部组合订阅合计；降低额度后，订阅输出也会受限。</p><label v-if="editing && editing.id !== 1" class="checkbox-label"><input v-model="form.enabled" type="checkbox" />启用账号</label>
      </fieldset><p v-if="formError" class="notice error" role="alert">{{ formError }}</p><div class="dialog-actions"><button type="button" :disabled="saving" @click="open = false">取消</button><button class="primary" :disabled="saving || !valid || paused">{{ saving ? '保存中…' : '保存用户' }}</button></div></form>
    </Modal>
  </section>
</template>
