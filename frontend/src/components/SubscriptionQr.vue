<script setup lang="ts">
import { toastRef } from '../toast'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import QRCode from 'qrcode'
import { absoluteURL, copyText } from '../api/accounts'
import { errorText } from '../api/system'
import Modal from './Modal.vue'
import AppIcon from './AppIcon.vue'
import '../community.css'
const props = withDefaults(defineProps<{ url: string; title: string; format?: string }>(), { format: 'clash' })
const emit = defineEmits<{ close: []; 'update:format': [value: string] }>()
const format = ref(props.format)
const image = ref('')
const error = ref('')
const message = toastRef()
const generating = ref(false)
const formats = [
  { value: 'clash', label: 'Clash / Mihomo', desc: '完整配置，包含规则组合与按国家的分组，推荐 Clash Verge、FlClash 等扫码导入。' },
  { value: 'auto', label: '自动识别', desc: '服务器按客户端返回合适的格式，适合不确定客户端类型时使用。' },
  { value: 'v2ray', label: 'v2ray URI', desc: '只包含节点 URI，适合 v2rayN、Shadowrocket；规则组合不生效。' },
]
const link = computed(() => {
  const value = new URL(absoluteURL(props.url))
  value.searchParams.delete('format')
  if (format.value !== 'auto') value.searchParams.set('format', format.value)
  return value.href
})
const current = computed(() => formats.find(item => item.value === format.value) ?? formats[0])
function choose(value: string) { format.value = value; emit('update:format', value) }
function selectAll(event: Event) { (event.target as HTMLInputElement).select() }
let sequence = 0
watch(link, async value => {
  const run = ++sequence
  image.value = ''; error.value = ''; generating.value = true
  try {
    const data = await QRCode.toDataURL(value, { width: 384, margin: 1, errorCorrectionLevel: 'M', color: { dark: '#101820', light: '#ffffff' } })
    if (run === sequence) image.value = data
  } catch (cause) { if (run === sequence) error.value = `二维码生成失败：${errorText(cause)}` }
  finally { if (run === sequence) generating.value = false }
}, { immediate: true })
onBeforeUnmount(() => { sequence++ })
async function copy() { try { await copyText(link.value); message.value = `${current.value.label} 订阅链接已复制。` } catch (cause) { error.value = errorText(cause) } }
</script>
<template>
  <Modal :title="title" wide @close="emit('close')">
    <div class="qr-layout">
      <div class="qr-card">
        <div class="qr-frame"><img v-if="image" :src="image" :alt="`${current.label} 订阅链接二维码`" width="192" height="192" /><p v-else-if="generating" role="status"><span class="spinner"></span>正在生成…</p></div>
        <span class="qr-caption"><AppIcon name="camera" />用手机客户端扫一扫</span>
      </div>
      <div class="qr-side">
        <span class="sub-label">订阅格式</span>
        <div class="scope-tabs" role="group" aria-label="二维码订阅格式"><button v-for="item in formats" :key="item.value" type="button" :aria-pressed="format === item.value" @click="choose(item.value)">{{ item.label }}</button></div>
        <p class="small muted">{{ current.desc }}</p>
        <span class="sub-label">二维码中的链接</span>
        <div class="link-bar"><span class="link-bar-field"><AppIcon name="link" /><input :value="link" aria-label="二维码对应订阅链接" readonly @focus="selectAll" /></span><span class="link-bar-actions"><button type="button" class="primary compact" @click="copy"><AppIcon name="copy" />复制</button></span></div>
        <div class="qr-note"><AppIcon name="lock" /><span>二维码在浏览器本地生成，不会上传。链接包含你的个人令牌，只向自己的设备展示；怀疑泄露时可在订阅卡片里重置链接。</span></div>
        <p v-if="error" class="notice error" role="alert">{{ error }}</p>
      </div>
    </div>
    <div class="dialog-actions"><button type="button" class="primary" @click="emit('close')">完成</button></div>
  </Modal>
</template>
