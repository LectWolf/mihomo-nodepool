<script setup lang="ts">
import { computed, ref } from 'vue'
import FilterPopover from './FilterPopover.vue'
import AppIcon from './AppIcon.vue'
const props = defineProps<{ modelValue: string[]; options: { code: string; name: string; count?: number }[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
const flags = import.meta.glob<string>('../../node_modules/flag-icons/flags/4x3/[a-z][a-z].svg', { eager: true, query: '?url&no-inline', import: 'default' })
const search = ref('')
const names = new Intl.DisplayNames(['zh-CN'], { type: 'region' })
function label(code: string, fallback: string) { try { return names.of(code) || fallback || code } catch { return fallback || code } }
const items = computed(() => props.options.map(v => ({ ...v, name: label(v.code, v.name) })).filter(v => `${v.code} ${v.name}`.toLocaleLowerCase().includes(search.value.toLocaleLowerCase())))
const text = computed(() => !props.modelValue.length ? '国家 / 地区' : props.modelValue.length === 1 ? label(props.modelValue[0]!, '') : `地区 · ${props.modelValue.length}`)
function toggle(code: string) { emit('update:modelValue', props.modelValue.includes(code) ? props.modelValue.filter(v => v !== code) : [...props.modelValue, code]) }
function keydown(event: KeyboardEvent) {
  const button = event.target as HTMLElement
  const grid = button.closest('.region-grid'); if (!grid || !['ArrowRight','ArrowLeft','ArrowDown','ArrowUp','Home','End'].includes(event.key)) return
  event.preventDefault(); const buttons = Array.from(grid.querySelectorAll<HTMLButtonElement>('button')); const index = buttons.indexOf(button as HTMLButtonElement)
  const columns = getComputedStyle(grid).gridTemplateColumns.split(' ').length
  const step = event.key === 'ArrowDown' ? columns : event.key === 'ArrowUp' ? -columns : event.key === 'ArrowRight' ? 1 : -1
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : Math.max(0, Math.min(buttons.length-1, index+step))
  buttons[next]?.focus()
}
</script>
<template>
  <FilterPopover label="国家或地区" :text="text" :active="modelValue.length > 0" :width="760">
    <div class="popup-heading"><strong>国家 / 地区</strong><button type="button" class="text-button" :disabled="!modelValue.length" @click="emit('update:modelValue', [])">清空</button></div>
    <label class="search-field popup-search"><span class="sr-only">搜索地区</span><AppIcon name="search" /><input v-model="search" type="search" placeholder="搜索国家、地区或代码" /></label>
    <div class="region-grid" @keydown="keydown"><button type="button" class="region-cell" :class="{ on: !modelValue.length }" :aria-pressed="!modelValue.length" @click="emit('update:modelValue', [])"><AppIcon name="nodes" /><span>全部</span></button><button v-for="country in items" :key="country.code" type="button" class="region-cell" :title="country.name" :aria-label="country.name" :class="{ on: modelValue.includes(country.code) }" :aria-pressed="modelValue.includes(country.code)" @click="toggle(country.code)"><img v-if="flags[`../../node_modules/flag-icons/flags/4x3/${country.code.toLowerCase()}.svg`]" :src="flags[`../../node_modules/flag-icons/flags/4x3/${country.code.toLowerCase()}.svg`]" alt="" width="20" height="15" /><AppIcon v-else name="globe" /><span>{{ country.name }}</span><small v-if="country.count !== undefined">{{ country.count }}</small></button></div>
    <p v-if="!items.length" class="small muted popup-empty">没有匹配的地区</p>
  </FilterPopover>
</template>
