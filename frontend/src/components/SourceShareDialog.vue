<script setup lang="ts">
import { computed, ref } from 'vue'
import { errorText, formatDate, isCancelled, json, useApi, type Source } from '../api/system'
import { useAccountScope } from '../community'
import { toastRef } from '../toast'
import Modal from './Modal.vue'
import UserAvatar from './UserAvatar.vue'
import AppIcon from './AppIcon.vue'
import '../community.css'
interface Share { user_id: number; username: string; display_name: string; avatar_version: number; enabled: boolean; seated: boolean; created_at: string }
const props = defineProps<{ source: Source }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const api = useApi()
const scope = useAccountScope()
const items = ref<Share[]>([])
const limit = ref(200)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const target = ref('')
const removing = ref<Share | null>(null)
const message = toastRef()
const full = computed(() => items.value.length >= limit.value)
async function load() {
  const ticket = scope.ticket()
  loading.value = true
  try {
    const value = await api<{ items: Share[]; limit: number }>(`/sources/${props.source.id}/shares`)
    if (!scope.current(ticket)) return
    items.value = value.items ?? []; limit.value = value.limit || 200; error.value = ''
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { loading.value = false }
}
async function add() {
  const user = target.value.trim()
  if (!user || busy.value || full.value) return
  const ticket = scope.ticket()
  busy.value = true; error.value = ''
  try {
    const share = await api<Share>(`/sources/${props.source.id}/shares`, json('POST', { user }))
    if (!scope.current(ticket)) return
    target.value = ''; message.value = `已分享给 ${share.display_name || share.username}`
    emit('changed'); await load()
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
async function remove(share: Share) {
  if (busy.value) return
  const ticket = scope.ticket()
  busy.value = true; error.value = ''
  try {
    await api(`/sources/${props.source.id}/shares/${share.user_id}`, json('DELETE'))
    if (!scope.current(ticket)) return
    removing.value = null; message.value = '已取消分享，该用户的组合不再输出此来源。'
    emit('changed'); await load()
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
void load()
</script>
<template>
  <Modal title="分享私有来源" :busy="busy" @close="emit('close')">
    <h3 class="ellipsis" :title="source.name">{{ source.name }}</h3>
    <p class="small muted">{{ source.private ? '私有来源只对你和下方用户可见，会出现在对方「专属」里，不占对方来源额度，也不计分享奖励。' : '这个来源目前是公开的。名单会保留，改为私有后只有名单内的用户可以继续使用。' }}</p>
    <form class="share-add" @submit.prevent="add">
      <label class="search-field"><span class="sr-only">用户 ID 或用户名</span><AppIcon name="users" /><input v-model="target" placeholder="输入用户 ID（如 #12）或用户名" maxlength="64" :disabled="busy || full" autocomplete="off" /></label>
      <button class="primary" type="submit" :disabled="busy || full || !target.trim()"><AppIcon name="plus" />添加</button>
    </form>
    <p class="small muted">对方可以在「我的账号」或右上角账号菜单里查看自己的用户 ID。已分享 {{ items.length }} / {{ limit }} 人。</p>
    <p v-if="error" class="notice error small" role="alert">{{ error }}</p>
    <p v-if="loading && !items.length" class="small muted" role="status">正在读取分享名单…</p>
    <ul v-else-if="items.length" class="share-list" :aria-busy="loading">
      <li v-for="share in items" :key="share.user_id">
        <UserAvatar :id="share.user_id" :name="share.display_name || share.username" :version="share.avatar_version" :size="32" />
        <span class="share-name"><strong :title="share.display_name || share.username">{{ share.display_name || share.username }}</strong><small class="muted">#{{ share.user_id }} · {{ share.username }} · {{ formatDate(share.created_at).split(' ')[0] }} 分享</small></span>
        <span v-if="!share.enabled" class="mini-tag red">已停用</span><span v-else-if="share.seated" class="mini-tag green">使用中</span>
        <template v-if="removing?.user_id === share.user_id"><button class="compact" :disabled="busy" @click="removing = null">取消</button><button class="compact danger" :disabled="busy" @click="remove(share)">确认移除</button></template>
        <button v-else class="compact" :disabled="busy" :aria-label="`取消分享给 ${share.display_name || share.username}`" @click="removing = share">移除</button>
      </li>
    </ul>
    <p v-else-if="!error" class="small muted">还没有分享给任何人。</p>
  </Modal>
</template>
<style scoped>
.share-add { display: flex; gap: 8px; margin: 12px 0 6px; }
.share-add .search-field { flex: 1; min-width: 0; }
.share-add .search-field input { width: 100%; }
.share-list { list-style: none; margin: 10px 0 0; padding: 0; max-height: 360px; overflow: auto; }
.share-list li { display: flex; align-items: center; gap: 10px; padding: 9px 0; border-bottom: 1px solid var(--line); }
.share-list li:last-child { border-bottom: 0; }
.share-name { flex: 1; min-width: 0; display: grid; }
.share-name strong, .share-name small { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
</style>
