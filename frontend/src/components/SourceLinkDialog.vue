<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { absoluteURL, copyText, currentUser } from '../api/accounts'
import { errorText, isCancelled, json, useApi, type Source } from '../api/system'
import { useAccountScope } from '../community'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import SubscriptionQr from './SubscriptionQr.vue'
const props = defineProps<{ source: Source }>()
const emit = defineEmits<{ close: []; updated: [source: Source] }>()
const api = useApi()
const scope = useAccountScope()
const path = ref(props.source.share_url || '')
const error = ref('')
const message = ref('')
const confirming = ref(false)
const busy = ref(false)
const qr = ref(false)
const allowed = computed(() => currentUser.value?.role === 'admin' || currentUser.value?.id === props.source.owner_id)
watch(() => props.source.share_url, value => { path.value = value || ''; qr.value = false })
async function copy() { try { await copyText(absoluteURL(path.value)); message.value = '来源专属直链已复制。' } catch (cause) { error.value = errorText(cause) } }
async function rotate() {
  if (!confirming.value || busy.value || !allowed.value) return
  const ticket = scope.ticket()
  busy.value = true; error.value = ''; message.value = ''
  try {
    const result = await api<{ url: string }>(`/sources/${props.source.id}/link/rotate`, json('POST'))
    if (!scope.current(ticket)) return
    path.value = result.url; qr.value = false; confirming.value = false; message.value = '来源直链已重置，旧链接立即失效。'
    emit('updated', { ...props.source, share_url: result.url })
  } catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { busy.value = false }
}
</script>
<template>
  <Modal title="来源专属订阅直链" :busy="busy" @close="emit('close')">
    <h3 class="ellipsis" :title="source.name">{{ source.name }}</h3>
    <p class="notice warning">持有此直链即可下载整个来源，绕过个人等级和席位限制。仅来源所有者和管理员可以获取，请仅向可信对象分享。</p>
    <template v-if="allowed && path">
      <div class="copy-field"><input :value="absoluteURL(path)" aria-label="来源专属订阅直链" readonly /><button class="icon-button" aria-label="复制来源直链" title="复制来源直链" @click="copy"><AppIcon name="copy" /></button></div>
      <div class="source-community-actions"><button @click="qr = true">二维码 / 选择格式</button><button class="text-button danger" :disabled="busy" @click="confirming = true; error = ''"><AppIcon name="reset" />重置直链</button></div>
    </template>
    <p v-else class="small muted">当前未提供来源直链，请重新读取来源列表确认权限。</p>
    <div v-if="confirming" class="subscriber-confirm" role="alertdialog" aria-label="确认重置来源直链"><p>重置后，所有使用旧直链的客户端将无法继续下载。确认后请分发新链接。</p><div class="dialog-actions"><button :disabled="busy" @click="confirming = false">取消</button><button class="danger-solid" :disabled="busy" @click="rotate">确认重置来源直链</button></div></div>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p><p v-if="message" class="notice success" role="status">{{ message }}</p>
    <div class="dialog-actions"><button :disabled="busy" @click="emit('close')">关闭</button></div>
    <SubscriptionQr v-if="qr && path && allowed" :url="path" title="来源直链二维码" @close="qr = false" />
  </Modal>
</template>
