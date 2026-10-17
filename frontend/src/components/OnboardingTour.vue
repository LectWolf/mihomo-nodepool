<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
export interface TourStep { target?: string; title: string; body: string; icon?: string; before?: () => void }
const props = defineProps<{ steps: TourStep[] }>()
const emit = defineEmits<{ close: [finished: boolean] }>()
const index = ref(0)
const rect = ref<DOMRect | null>(null)
const card = ref<HTMLElement>()
const cardStyle = ref<Record<string, string>>({})
const step = computed(() => props.steps[index.value])
const last = computed(() => index.value === props.steps.length - 1)
const pad = 6
// 目标元素不可见（如手机上被折叠）时退化为居中卡片
function visible(el: Element | null): el is HTMLElement {
  if (!(el instanceof HTMLElement)) return false
  const r = el.getBoundingClientRect()
  return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== 'hidden'
}
function target() {
  const s = step.value?.target
  if (!s) return null
  const found = [...document.querySelectorAll(s)].find(visible)
  return found ?? [...document.querySelectorAll('[data-tour="nav-more"]')].find(el => s.startsWith('[data-tour="nav-') && visible(el)) ?? null
}
function measure() {
  const el = target()
  rect.value = el ? el.getBoundingClientRect() : null
  void nextTick(place)
}
function place() {
  const r = rect.value, box = card.value
  const w = window.innerWidth, h = window.innerHeight
  if (!box) return
  if (!r || w < 640) { cardStyle.value = r && w < 640 ? { left: '12px', right: '12px', bottom: '12px' } : { left: '50%', top: '50%', transform: 'translate(-50%, -50%)' }; return }
  const cw = box.offsetWidth, ch = box.offsetHeight, gap = 14
  let top = r.bottom + gap, left = r.left
  if (r.right + gap + cw < w && r.height > 120) { left = r.right + gap; top = r.top }
  else if (top + ch > h - 12) top = Math.max(12, r.top - gap - ch)
  if (r.width < 260 && r.left < 280 && r.right + gap + cw < w) { left = r.right + gap; top = Math.min(Math.max(12, r.top + r.height / 2 - ch / 2), h - ch - 12) }
  left = Math.min(Math.max(12, left), w - cw - 12)
  cardStyle.value = { left: `${left}px`, top: `${Math.min(Math.max(12, top), h - ch - 12)}px` }
}
async function show() {
  step.value?.before?.()
  await nextTick(); await new Promise(resolve => setTimeout(resolve, 60))
  const el = target()
  el?.scrollIntoView({ block: 'nearest', inline: 'center' })
  measure()
  card.value?.querySelector<HTMLButtonElement>('.tour-next')?.focus()
}
function next() { if (last.value) emit('close', true); else index.value++ }
function prev() { if (index.value > 0) index.value-- }
function key(event: KeyboardEvent) {
  if (event.key === 'Escape') emit('close', false)
  else if (event.key === 'ArrowRight') next()
  else if (event.key === 'ArrowLeft') prev()
}
watch(index, () => { void show() })
let frame = 0
function schedule() { cancelAnimationFrame(frame); frame = requestAnimationFrame(measure) }
onMounted(() => { void show(); window.addEventListener('resize', schedule); window.addEventListener('scroll', schedule, true); document.addEventListener('keydown', key) })
onBeforeUnmount(() => { cancelAnimationFrame(frame); window.removeEventListener('resize', schedule); window.removeEventListener('scroll', schedule, true); document.removeEventListener('keydown', key) })
const spot = computed(() => rect.value ? { left: `${rect.value.left - pad}px`, top: `${rect.value.top - pad}px`, width: `${rect.value.width + pad * 2}px`, height: `${rect.value.height + pad * 2}px` } : null)
</script>

<template>
  <Teleport to="body">
    <div class="tour-layer" role="dialog" aria-modal="true" :aria-label="`新手引导：${step?.title}`">
      <div v-if="spot" class="tour-spot" :style="spot"></div>
      <div v-else class="tour-dim"></div>
      <div ref="card" class="tour-card" :style="cardStyle">
        <div class="tour-card-head"><span class="tour-icon"><AppIcon :name="step?.icon || 'compass'" /></span><span class="tour-count">{{ index + 1 }} / {{ steps.length }}</span><button type="button" class="icon-button tour-skip" aria-label="跳过教程" title="跳过教程" @click="emit('close', false)"><AppIcon name="close" /></button></div>
        <h3>{{ step?.title }}</h3>
        <p>{{ step?.body }}</p>
        <div class="tour-progress" aria-hidden="true"><i v-for="(_, i) in steps" :key="i" :class="{ on: i <= index }"></i></div>
        <div class="tour-actions"><button type="button" class="text-button" @click="emit('close', false)">跳过</button><span></span><button v-if="index > 0" type="button" class="compact" @click="prev">上一步</button><button type="button" class="primary compact tour-next" @click="next">{{ last ? '开始使用' : '下一步' }}<AppIcon v-if="!last" name="right" /></button></div>
      </div>
    </div>
  </Teleport>
</template>
