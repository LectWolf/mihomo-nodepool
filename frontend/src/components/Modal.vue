<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'
import { raiseToasts } from '../toast'
const props = withDefaults(defineProps<{ title: string; busy?: boolean; wide?: boolean; expanded?: boolean; locked?: boolean }>(), { busy: false, wide: false, expanded: false, locked: false })
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
const titleId = useId()
let previousFocus: HTMLElement | null = null
let press: { id: number; x: number; y: number; outside: boolean; moved: boolean } | null = null
let backdropClick = false
function outside(event: PointerEvent) {
  const rect = dialog.value?.getBoundingClientRect()
  return !!rect && (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom)
}
function start(event: PointerEvent) {
  backdropClick = false
  press = event.isPrimary && event.button === 0 ? { id: event.pointerId, x: event.clientX, y: event.clientY, outside: event.target === dialog.value && outside(event), moved: false } : null
}
function move(event: PointerEvent) { if (press?.id === event.pointerId && Math.hypot(event.clientX - press.x, event.clientY - press.y) > 6) press.moved = true }
function finish(event: PointerEvent) {
  backdropClick = press?.id === event.pointerId && press.outside && !press.moved && outside(event)
  press = null
}
function clickBackdrop() { const close = backdropClick; backdropClick = false; if (close && !props.busy && !props.locked) emit('close') }
onMounted(() => { previousFocus = document.activeElement as HTMLElement; dialog.value?.showModal(); raiseToasts() })
onBeforeUnmount(() => { dialog.value?.close(); previousFocus?.focus() })
</script>

<template>
  <dialog ref="dialog" :class="['dialog', { wide, expanded }]" :aria-labelledby="titleId" @cancel.prevent="!busy && !locked && emit('close')" @pointerdown.capture="start" @pointermove.capture="move" @pointerup.capture="finish" @pointercancel="press = null; backdropClick = false" @click="clickBackdrop">
    <div class="dialog-head"><h2 :id="titleId">{{ title }}</h2><button v-if="!locked" class="icon-button" type="button" aria-label="关闭弹窗" :disabled="busy" @click="emit('close')"><AppIcon name="close" /></button></div>
    <slot />
  </dialog>
</template>
