<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import '../pagination.css'

const props = withDefaults(defineProps<{
  modelValue: number
  pages: number
  disabled?: boolean
  label?: string
  previousLabel?: string
  nextLabel?: string
}>(), { disabled: false, label: '分页', previousLabel: '上一页', nextLabel: '下一页' })
const emit = defineEmits<{ 'update:modelValue': [page: number] }>()
const totalPages = computed(() => Number.isFinite(props.pages) ? Math.max(1, Math.floor(props.pages)) : 1)
const current = computed(() => Number.isFinite(props.modelValue) ? Math.min(totalPages.value, Math.max(1, Math.floor(props.modelValue))) : 1)
const items = computed(() => {
  const visible = new Set([1, totalPages.value])
  for (let page = Math.max(1, current.value - 2); page <= Math.min(totalPages.value, current.value + 2); page++) visible.add(page)
  const result: (number | string)[] = []
  let previous = 0
  for (const page of [...visible].sort((a, b) => a - b)) {
    // 只隔一页时直接显示页码，省略号至少代表两页
    if (previous && page - previous === 2) result.push(previous + 1)
    else if (previous && page - previous > 2) result.push(`gap-${previous}`)
    result.push(page)
    previous = page
  }
  return result
})
function select(page: number) {
  if (!props.disabled && page >= 1 && page <= totalPages.value && page !== props.modelValue) emit('update:modelValue', page)
}
</script>

<template>
  <nav class="page-numbers" :aria-label="label">
    <button type="button" class="page-number page-arrow" :aria-label="previousLabel" :title="previousLabel" :disabled="disabled || current <= 1" @click="select(current - 1)"><AppIcon name="left" /></button>
    <template v-for="item in items" :key="item">
      <button v-if="typeof item === 'number'" type="button" class="page-number" :class="{ primary: item === current }" :aria-label="`第 ${item} 页`" :aria-current="item === current ? 'page' : undefined" :disabled="disabled" @click="select(item)">{{ item }}</button>
      <span v-else class="page-ellipsis" aria-hidden="true">…</span>
    </template>
    <button type="button" class="page-number page-arrow" :aria-label="nextLabel" :title="nextLabel" :disabled="disabled || current >= totalPages" @click="select(current + 1)"><AppIcon name="right" /></button>
  </nav>
</template>
