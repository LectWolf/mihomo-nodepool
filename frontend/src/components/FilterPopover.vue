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
function close(restore = true) {
  ++sequence
  opened.value = false
  document.removeEventListener('pointerdown', outside, true)
  document.removeEventListener('focusin', focusChanged, true)
  window.removeEventListener('resize', resized)
  window.removeEventListener('scroll', scrolled, true)
  panel.value?.hidePopover()
  if (restore) trigger.value?.focus({ preventScroll: true })
}
function outside(event: Event) { if (!event.composedPath().includes(trigger.value!) && !event.composedPath().includes(panel.value!)) close(false) }
function focusChanged(event: Event) { if (event.target instanceof Node && !panel.value?.contains(event.target) && event.target !== trigger.value) close(false) }
function resized() { close(false) }
function scrolled(event: Event) { if (event.target instanceof Node && panel.value?.contains(event.target)) return; close(false) }
async function open() {
  if (opened.value) { close(); return }
  const current = ++sequence
  target.value = trigger.value?.closest('dialog') ?? 'body'; opened.value = true
  await nextTick()
  if (!opened.value || sequence !== current || !panel.value || !trigger.value) return
  panel.value.showPopover()
  const r = trigger.value.getBoundingClientRect()
  const width = Math.min(props.width, innerWidth - 20)
  const height = Math.min(panel.value.scrollHeight + 2, innerHeight - 24, 440)
  const top = r.bottom + height + 8 <= innerHeight ? r.bottom + 6 : Math.max(10, r.top - height - 6)
  placement.value = { left: `${Math.max(10, Math.min(r.left, innerWidth - width - 10))}px`, top: `${top}px`, width: `${width}px`, maxHeight: `${Math.max(100, Math.min(440, innerHeight - top - 10))}px` }
  await nextTick()
  panel.value?.querySelector<HTMLElement>('input:not(:disabled),button:not(:disabled)')?.focus({ preventScroll: true })
  document.addEventListener('pointerdown', outside, true)
  document.addEventListener('focusin', focusChanged, true)
  window.addEventListener('resize', resized)
  requestAnimationFrame(() => { if (sequence === current && opened.value) window.addEventListener('scroll', scrolled, true) })
}
function escape(event: KeyboardEvent) { if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close() } }
onBeforeUnmount(() => close(false))
</script>
<template>
  <div class="filter-popover">
    <button ref="trigger" type="button" class="filter-trigger" :class="{ chosen: active }" :aria-label="label" :aria-expanded="opened" :aria-controls="`${id}-panel`" aria-haspopup="dialog" @click="open" @keydown.down.prevent="open" @keydown.esc="escape"><span>{{ text }}</span><AppIcon name="down" /></button>
    <Teleport :to="target"><div v-if="opened" :id="`${id}-panel`" ref="panel" class="filter-popup" popover="manual" role="dialog" :aria-label="label" :style="placement" @keydown="escape"><slot :close="close" /></div></Teleport>
  </div>
</template>
