<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import QRCode from 'qrcode'
import { absoluteURL, copyText } from '../api/accounts'
import { errorText } from '../api/system'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import CustomSelect from './CustomSelect.vue'
import '../community.css'
const props = defineProps<{ url: string; title: string }>()
const emit = defineEmits<{ close: [] }>()
const format = ref('auto')
const image = ref('')
const error = ref('')
const message = ref('')
const generating = ref(false)
const formats = [{ value: 'auto', label: '自动识别' }, { value: 'clash', label: 'Clash / Mihomo 完整配置' }, { value: 'v2ray', label: 'v2ray URI' }]
const link = computed(() => {
  const value = new URL(absoluteURL(props.url))
  value.searchParams.delete('format')
  if (format.value !== 'auto') value.searchParams.set('format', format.value)
  return value.href
})
const formatName = computed(() => formats.find(item => item.value === format.value)?.label)
let sequence = 0
watch(link, async value => {
  const current = ++sequence
  image.value = ''; error.value = ''; message.value = ''; generating.value = true
  try {
    const data = await QRCode.toDataURL(value, { width: 320, margin: 4, errorCorrectionLevel: 'M', color: { dark: '#101820', light: '#ffffff' } })
    if (current === sequence) image.value = data
  } catch (cause) { if (current === sequence) error.value = `二维码生成失败：${errorText(cause)}` }
  finally { if (current === sequence) generating.value = false }
}, { immediate: true })
onBeforeUnmount(() => { sequence++ })
async function copy() { try { await copyText(link.value); message.value = '当前格式的链接已复制。' } catch (cause) { error.value = errorText(cause) } }
</script>
<template>
  <Modal :title="title" @close="emit('close')">
    <div class="qr-format"><span class="small muted">订阅格式</span><CustomSelect v-model="format" label="二维码订阅格式" :options="formats" /></div>
    <div class="qr-image"><img v-if="image" :src="image" :alt="`${formatName} 订阅链接二维码`" width="320" height="320" /><p v-else-if="generating" role="status"><span class="spinner"></span>正在本地生成二维码…</p></div>
    <p class="small"><strong>{{ formatName }}</strong> · 二维码包含下方完整链接</p>
    <div class="copy-field"><input :value="link" aria-label="二维码对应订阅链接" readonly /><button class="icon-button" title="复制当前格式链接" aria-label="复制当前格式链接" @click="copy"><AppIcon name="copy" /></button></div>
    <p class="small muted">二维码完全在浏览器本地生成。链接含访问令牌，请仅向可信设备展示；Mihomo 完整配置首选，v2ray URI 不含规则且不保证全部参数兼容。</p>
    <p v-if="error" class="notice error" role="alert">{{ error }}</p><p v-if="message" class="notice success" role="status">{{ message }}</p>
    <div class="dialog-actions"><button @click="emit('close')">关闭</button></div>
  </Modal>
</template>
