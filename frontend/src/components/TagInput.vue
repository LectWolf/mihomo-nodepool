<script setup lang="ts">
import { computed, ref } from 'vue'
import AppIcon from './AppIcon.vue'
// 标签输入：回车、空格、逗号或失焦时生成标签，退格删除最后一个
const props = withDefaults(defineProps<{ modelValue: string[]; placeholder?: string; label: string; normalize?: (raw: string) => string; validate?: (value: string) => boolean }>(), { placeholder: '' })
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
const text = ref('')
const flash = ref('')
const error = ref('')
const input = ref<HTMLInputElement | null>(null)
let flashTimer: ReturnType<typeof setTimeout> | undefined
const tags = computed(() => props.modelValue)
function clean(raw: string) { return props.normalize ? props.normalize(raw) : raw.trim() }
function add(raw: string) {
  const parts = raw.split(/[\s,，;；、]+/).map(clean).filter(Boolean)
  if (!parts.length) return
  const next = [...tags.value]
  const bad: string[] = []
  for (const part of parts) {
    if (props.validate && !props.validate(part)) { bad.push(part); continue }
    if (next.includes(part)) { highlight(part); continue }
    next.push(part)
  }
  error.value = bad.length ? `格式不正确：${bad.join('、')}` : ''
  if (next.length !== tags.value.length) emit('update:modelValue', next)
  text.value = bad.join(' ')
}
function highlight(tag: string) { flash.value = tag; clearTimeout(flashTimer); flashTimer = setTimeout(() => { flash.value = '' }, 900) }
function remove(index: number) { emit('update:modelValue', tags.value.filter((_, i) => i !== index)); input.value?.focus() }
function onKey(event: KeyboardEvent) {
  if (event.isComposing) return
  if (['Enter', ' ', ',', '，', ';', 'Tab'].includes(event.key)) {
    if (event.key === 'Tab' && !text.value.trim()) return
    event.preventDefault(); add(text.value)
  } else if (event.key === 'Backspace' && !text.value && tags.value.length) {
    event.preventDefault(); remove(tags.value.length - 1)
  }
}
function onPaste(event: ClipboardEvent) {
  const data = event.clipboardData?.getData('text') ?? ''
  if (/[\s,，;；、]/.test(data)) { event.preventDefault(); add(text.value + data) }
}
</script>

<template>
  <div class="tag-input-wrap">
    <div class="tag-input" @click="input?.focus()">
      <span v-for="(tag, index) in tags" :key="tag" :class="['tag-chip', { flash: flash === tag }]"><span>{{ tag }}</span><button type="button" :aria-label="`移除 ${tag}`" @click.stop="remove(index)"><AppIcon name="close" /></button></span>
      <input ref="input" v-model="text" :aria-label="label" :placeholder="tags.length ? '' : placeholder" spellcheck="false" autocapitalize="off" @keydown="onKey" @paste="onPaste" @blur="add(text)" />
    </div>
    <p v-if="error" class="field-hint danger-text">{{ error }}</p>
  </div>
</template>
