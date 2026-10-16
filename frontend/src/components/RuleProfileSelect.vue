<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { errorText, isCancelled, useApi } from '../api/system'
import type { RuleProfile } from '../api/accounts'
import CustomSelect from './CustomSelect.vue'
const props = withDefaults(defineProps<{ modelValue: number | null; disabled?: boolean }>(), { disabled: false })
const emit = defineEmits<{ 'update:modelValue': [value: number | null] }>()
const api = useApi()
const items = ref<RuleProfile[]>([])
const error = ref('')
const loading = ref(false)
const value = computed({ get: () => props.modelValue ?? '', set: (v: string | number) => emit('update:modelValue', v === '' ? null : Number(v)) })
const options = computed(() => [{ value: '' as string | number, label: '不绑定 · 仅节点配置' }, ...items.value.map(item => ({ value: item.id, label: item.name })), ...(props.modelValue && !items.value.some(item => item.id === props.modelValue) ? [{ value: props.modelValue, label: `规则组合 #${props.modelValue}` }] : [])])
async function load() {
  loading.value = true
  try { items.value = (await api<{ items: RuleProfile[] }>('/rule-profiles')).items; error.value = '' }
  catch (cause) { if (!isCancelled(cause)) error.value = errorText(cause) }
  finally { loading.value = false }
}
onMounted(load)
</script>
<template>
  <div class="form-field">
    <span class="field-label">规则组合 <small class="muted">可选 · 仅影响 Clash</small></span>
    <CustomSelect v-model="value" label="订阅规则组合" :options="options" :disabled="disabled || loading" />
    <span v-if="!error" class="field-hint">在「规则组合」中创建和修改分流规则与 DNS。</span>
    <span v-else role="alert" class="field-hint danger">{{ error }} <button type="button" class="text-button" @click="load">重试</button></span>
  </div>
</template>
