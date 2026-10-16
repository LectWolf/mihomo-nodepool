<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'
withDefaults(defineProps<{ title: string; busy?: boolean; wide?: boolean }>(), { busy: false, wide: false })
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
const titleId = useId()
let previousFocus: HTMLElement | null = null
onMounted(() => { previousFocus = document.activeElement as HTMLElement; dialog.value?.showModal() })
onBeforeUnmount(() => { dialog.value?.close(); previousFocus?.focus() })
</script>

<template>
  <dialog ref="dialog" :class="['dialog', { wide }]" :aria-labelledby="titleId" @cancel.prevent="!busy && emit('close')" @click="event => { if (event.target === dialog && !busy) emit('close') }">
    <div class="dialog-head"><h2 :id="titleId">{{ title }}</h2><button class="icon-button" type="button" aria-label="关闭弹窗" :disabled="busy" @click="emit('close')"><AppIcon name="close" /></button></div>
    <slot />
  </dialog>
</template>
