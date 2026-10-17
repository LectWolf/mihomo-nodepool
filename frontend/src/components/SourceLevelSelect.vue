<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { errorText, isCancelled, useApi } from '../api/system'
import { levelOptions, useAccountScope, type LevelDistribution } from '../community'
import CustomSelect from './CustomSelect.vue'
import '../community.css'
const props = defineProps<{ modelValue: number; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: number] }>()
const api = useApi()
const scope = useAccountScope()
const distribution = ref<LevelDistribution | null>(null)
const error = ref('')
const loading = ref(false)
const eligible = computed(() => distribution.value?.counts.slice(props.modelValue).reduce((total, count) => total + count, 0) ?? 0)
const percentage = computed(() => distribution.value?.total ? (eligible.value / distribution.value.total * 100).toFixed(1) : '0.0')
async function load() {
  if (loading.value) return
  const ticket = scope.ticket()
  loading.value = true
  try { const value = await api<LevelDistribution>('/levels'); if (scope.current(ticket)) { distribution.value = value; error.value = '' } }
  catch (cause) { if (scope.current(ticket) && !isCancelled(cause)) error.value = errorText(cause) }
  finally { loading.value = false }
}
onMounted(() => void load())
</script>
<template>
  <div class="form-field source-level-field">
    <span class="field-label">最低用户等级</span>
    <CustomSelect :model-value="modelValue" label="来源最低等级" :options="levelOptions" :disabled="disabled" @update:model-value="emit('update:modelValue', $event)" />
    <span class="field-hint">Lv0 默认不限。普通用户须达到所选等级方可领取；来源专属直链不受个人等级和席位限制。</span>
    <p v-if="distribution" class="small muted">全平台启用普通用户 {{ distribution.total }} 人，符合此门槛 {{ eligible }} 人（{{ percentage }}%）。</p>
    <div v-if="distribution" class="level-distribution"><span v-for="(count, index) in distribution.counts" :key="index" :title="`Lv${index} ${distribution.names[index] || ''} · ${distribution.thresholds[index] ?? 0} XP`"><strong>Lv{{ index }} · {{ count }} 人</strong>{{ distribution.total ? (count / distribution.total * 100).toFixed(1) : '0.0' }}%</span></div>
    <p v-if="error" class="small muted" role="status">等级分布读取失败：{{ error }}<button type="button" class="text-button" :disabled="loading" @click="load">重试</button></p>
  </div>
</template>
