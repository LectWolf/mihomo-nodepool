<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { dismissToast, onRaiseToasts, toasts } from '../toast'
import AppIcon from './AppIcon.vue'
const icons = { success: 'check', info: 'info', warning: 'alert', error: 'alert' } as const
const host = ref<HTMLElement>()
const supported = typeof HTMLElement !== 'undefined' && 'showPopover' in HTMLElement.prototype
// 弹窗处于浏览器顶层，toast 也放进顶层并在弹窗打开后重新置顶，避免被遮挡
function raise() {
  const el = host.value
  if (!supported || !el) return
  try {
    if (el.matches(':popover-open')) el.hidePopover()
    if (toasts.length) el.showPopover()
  } catch { /* 忽略不支持的环境 */ }
}
let stop: (() => void) | undefined
onMounted(() => { stop = onRaiseToasts(raise) })
onBeforeUnmount(() => stop?.())
watch(() => toasts.length, async (n, old) => { await nextTick(); if (!n || !old) raise() })
</script>

<template>
  <div ref="host" class="toast-host" aria-live="polite" :popover="supported ? 'manual' : undefined">
    <TransitionGroup name="toast">
      <div v-for="item in toasts" :key="item.id" :class="['toast', item.tone]" :role="item.tone === 'error' ? 'alert' : 'status'">
        <span class="toast-icon"><AppIcon :name="icons[item.tone]" /></span><span class="toast-text">{{ item.message }}</span><button v-if="item.action" type="button" class="toast-action" @click="item.action.run(); dismissToast(item.id)">{{ item.action.label }}</button>
        <button type="button" class="toast-close" aria-label="关闭提示" @click="dismissToast(item.id)"><AppIcon name="close" /></button>
      </div>
    </TransitionGroup>
  </div>
</template>
