<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { authOptions } from '../api/accounts'
// 邮箱输入：输入前缀时下拉推荐允许的后缀，例如 224 → 224@qq.com
const props = withDefaults(defineProps<{ modelValue: string; suffixes?: string[]; placeholder?: string; disabled?: boolean; autofocus?: boolean; autocomplete?: string; label?: string }>(), { placeholder: '', disabled: false, autofocus: false, autocomplete: 'email', label: '' })
const emit = defineEmits<{ 'update:modelValue': [value: string]; enter: [] }>()
const popular = ['qq.com', '163.com', 'gmail.com', 'foxmail.com', '126.com', 'outlook.com', 'aliyun.com', 'sina.com', '139.com', 'yahoo.com', '189.cn', 'sohu.com', 'icloud.com']
const focused = ref(false)
const active = ref(0)
const uid = `email-${Math.random().toString(36).slice(2, 8)}`
const pool = computed(() => {
  const allowed = (props.suffixes ?? authOptions.login.email_suffixes).map(v => v.trim().toLowerCase()).filter(Boolean)
  if (!allowed.length) return popular
  const rank = (v: string) => { const i = popular.indexOf(v); return i < 0 ? popular.length : i }
  return [...allowed].sort((a, b) => rank(a) - rank(b))
})
const suggestions = computed(() => {
  const raw = props.modelValue.trim().toLowerCase()
  if (!raw || /\s/.test(raw)) return []
  const at = raw.indexOf('@')
  if (at < 0) {
    // 只有前缀时跳过 edu.cn 这类通常需要子域名的后缀
    return pool.value.filter(v => !/^(edu|gov)\./.test(v)).slice(0, 6).map(v => `${raw}@${v}`)
  }
  const local = raw.slice(0, at), domain = raw.slice(at + 1)
  if (!local || domain.includes('@')) return []
  return pool.value.filter(v => v.startsWith(domain) && v !== domain).slice(0, 6).map(v => `${local}@${v}`)
})
const open = computed(() => focused.value && suggestions.value.length > 0)
watch(suggestions, () => { active.value = 0 })
function pick(value: string) { emit('update:modelValue', value); focused.value = false }
function onKey(event: KeyboardEvent) {
  if (!open.value) { if (event.key === 'Enter') emit('enter'); return }
  if (event.key === 'ArrowDown') { event.preventDefault(); active.value = (active.value + 1) % suggestions.value.length }
  else if (event.key === 'ArrowUp') { event.preventDefault(); active.value = (active.value - 1 + suggestions.value.length) % suggestions.value.length }
  else if (event.key === 'Enter' || event.key === 'Tab') { if (event.key === 'Enter') event.preventDefault(); pick(suggestions.value[active.value]) }
  else if (event.key === 'Escape') focused.value = false
}
</script>

<template>
  <span class="email-input">
    <input :value="modelValue" type="email" :autocomplete="autocomplete" :autofocus="autofocus" :disabled="disabled" :placeholder="placeholder" :aria-label="label || undefined"
      role="combobox" aria-autocomplete="list" :aria-expanded="open" :aria-controls="uid" :aria-activedescendant="open ? `${uid}-${active}` : undefined"
      spellcheck="false" autocapitalize="off" @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)" @focus="focused = true" @blur="focused = false" @keydown="onKey" />
    <ul v-if="open" :id="uid" class="email-suggest" role="listbox">
      <li v-for="(item, index) in suggestions" :id="`${uid}-${index}`" :key="item" role="option" :aria-selected="index === active" :class="{ active: index === active }" @mousedown.prevent="pick(item)" @mouseenter="active = index">
        <span class="email-local">{{ item.slice(0, item.indexOf('@')) }}</span><span class="email-domain">{{ item.slice(item.indexOf('@')) }}</span>
      </li>
    </ul>
  </span>
</template>
