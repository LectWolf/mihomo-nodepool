<script setup lang="ts">
import { ref } from 'vue'
import AppIcon from './AppIcon.vue'
// 拖入或点击选择文件，替代原生文件按钮
const props = withDefaults(defineProps<{ title: string; hint?: string; multiple?: boolean; disabled?: boolean; accept?: string; button?: string }>(), { hint: '', multiple: false, disabled: false, accept: '', button: '选择文件' })
const emit = defineEmits<{ files: [files: File[]] }>()
const input = ref<HTMLInputElement | null>(null)
const over = ref(false)
let depth = 0
function pick(event: Event) {
  const el = event.target as HTMLInputElement
  if (el.files?.length) emit('files', Array.from(el.files))
  el.value = ''
}
function enter() { if (props.disabled) return; depth++; over.value = true }
function leave() { depth = Math.max(0, depth - 1); if (!depth) over.value = false }
function drop(event: DragEvent) {
  depth = 0; over.value = false
  if (props.disabled) return
  const list = Array.from(event.dataTransfer?.files ?? [])
  if (list.length) emit('files', props.multiple ? list : list.slice(0, 1))
}
</script>

<template>
  <div :class="['file-drop', 'drop-zone', { over, disabled }]" @dragenter.prevent="enter" @dragover.prevent @dragleave.prevent="leave" @drop.prevent="drop">
    <span class="empty-icon"><AppIcon name="upload" /></span>
    <strong>{{ title }}</strong>
    <span class="small muted">{{ over ? '松开即可添加' : hint || '把文件拖到这里，或点击下方按钮选择' }}</span>
    <button type="button" class="compact" :disabled="disabled" @click="input?.click()"><AppIcon name="plus" />{{ button }}</button>
    <input ref="input" class="sr-only" type="file" tabindex="-1" aria-hidden="true" :multiple="multiple" :accept="accept || undefined" :disabled="disabled" @change="pick" />
  </div>
</template>
