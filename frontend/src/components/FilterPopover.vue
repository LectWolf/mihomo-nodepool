<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'
const props = withDefaults(defineProps<{ label: string; text: string; width?: number; active?: boolean }>(), { width: 430 })
const id = useId()
const trigger = ref<HTMLButtonElement>()
const panel = ref<HTMLDivElement>()
const target = ref<HTMLElement | string>('body')
const opened = ref(false)
const placement = ref<Record<string, string>>({})
let sequence = 0
function listen(add: boolean) {
  const action = add ? 'addEventListener' : 'removeEventListener'
  document[action]('pointerdown', outside, true)
  document[action]('focusin', focusChanged, true)
  window[action]('resize', place)
  window.visualViewport?.[action]('resize', place)
  window.visualViewport?.[action]('scroll', place)
}
function close(restore = true) {
  ++sequence
  opened.value = false
  listen(false)
  window.removeEventListener('scroll', scrolled, true)
  panel.value?.hidePopover()
  if (restore) trigger.value?.focus({ preventScroll: true })
}
function outside(event: Event) { if (!event.composedPath().includes(trigger.value!) && !event.composedPath().includes(panel.value!)) close(false) }
function focusChanged(event: Event) { if (event.target instanceof Node && !panel.value?.contains(event.target) && event.target !== trigger.value) close(false) }
// 手机输入法弹出会改变视口尺寸并滚动页面：只重新定位，不关闭
function typing() { return !!panel.value && panel.value.contains(document.activeElement) && document.activeElement instanceof HTMLInputElement }
function scrolled(event: Event) {
  if (event.target instanceof Node && panel.value?.contains(event.target)) return
  if (typing()) { place(); return }
  close(false)
}
function place() {
  if (!opened.value || !panel.value || !trigger.value) return
  const r = trigger.value.getBoundingClientRect()
  const view = window.visualViewport
  const top0 = (view?.offsetTop ?? 0) + 10, left0 = (view?.offsetLeft ?? 0) + 10
  const viewW = view?.width ?? innerWidth, viewH = view?.height ?? innerHeight
  const width = Math.min(props.width, viewW - 20)
  const height = Math.min(panel.value.scrollHeight + 2, viewH - 24, 440)
  const below = r.bottom + height + 8 <= top0 - 10 + viewH
  const top = below ? r.bottom + 6 : Math.max(top0, r.top - height - 6)
  placement.value = { left: `${Math.max(left0, Math.min(r.left, left0 - 10 + viewW - width - 10))}px`, top: `${top}px`, width: `${width}px`, maxHeight: `${Math.max(100, Math.min(440, top0 - 10 + viewH - top - 10))}px` }
}
async function open() {
  if (opened.value) { close(); return }
  const current = ++sequence
  target.value = trigger.value?.closest('dialog') ?? 'body'; opened.value = true
  await nextTick()
  if (!opened.value || sequence !== current || !panel.value || !trigger.value) return
  panel.value.showPopover()
  place()
  await nextTick()
  // 触屏设备不自动聚焦搜索框，避免一打开就弹出输入法
  const coarse = matchMedia('(pointer: coarse)').matches
  const first = panel.value?.querySelector<HTMLElement>(coarse ? 'button:not(:disabled)' : 'input:not(:disabled),button:not(:disabled)')
  ;(first ?? panel.value)?.focus({ preventScroll: true })
  listen(true)
  requestAnimationFrame(() => { if (sequence === current && opened.value) window.addEventListener('scroll', scrolled, true) })
}
function escape(event: KeyboardEvent) { if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close() } }
onBeforeUnmount(() => close(false))
</script>
<template>
  <div class="filter-popover">
    <button ref="trigger" type="button" class="filter-trigger" :class="{ chosen: active }" :aria-label="label" :aria-expanded="opened" :aria-controls="`${id}-panel`" aria-haspopup="dialog" @click="open" @keydown.down.prevent="open" @keydown.esc="escape"><span>{{ text }}</span><AppIcon name="down" /></button>
    <Teleport :to="target"><div v-if="opened" :id="`${id}-panel`" ref="panel" class="filter-popup" popover="manual" role="dialog" tabindex="-1" :aria-label="label" :style="placement" @keydown="escape"><slot :close="close" /></div></Teleport>
  </div>
</template>
