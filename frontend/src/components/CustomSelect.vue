<script setup lang="ts" generic="T extends string | number">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'

export interface SelectOption<Value extends string | number = string | number> { value: Value; label: string; disabled?: boolean }
const props = withDefaults(defineProps<{
  modelValue: T
  options: readonly SelectOption<T>[]
  label: string
  disabled?: boolean
  placeholder?: string
  emptyText?: string
}>(), { disabled: false, placeholder: '请选择', emptyText: '暂无可选项' })
const emit = defineEmits<{ 'update:modelValue': [value: T] }>()
const id = useId()
const trigger = ref<HTMLButtonElement>()
const panel = ref<HTMLDivElement>()
const teleportTarget = ref<HTMLElement | string>('body')
const opened = ref(false)
const active = ref(-1)
const placement = ref<Record<string, string>>({})
const selected = computed(() => props.options.find(option => option.value === props.modelValue))
const activeId = computed(() => opened.value && active.value >= 0 ? `${id}-option-${active.value}` : undefined)
let search = ''
let searchTimer: ReturnType<typeof setTimeout> | undefined
let openSequence = 0
let observingScroll = false

function enabled(index: number) { return index >= 0 && index < props.options.length && !props.options[index]?.disabled }
function boundary(last = false) {
  for (let step = 0; step < props.options.length; step++) {
    const index = last ? props.options.length - 1 - step : step
    if (enabled(index)) return index
  }
  return -1
}
function reveal() {
  void nextTick(() => panel.value?.querySelector<HTMLElement>(`[data-index="${active.value}"]`)?.scrollIntoView({ block: 'nearest', behavior: 'instant' }))
}
function setActive(index: number) { active.value = index; reveal() }
function move(direction: number) {
  for (let step = 1; step <= props.options.length; step++) {
    const start = active.value < 0 ? (direction > 0 ? -1 : 0) : active.value
    const index = (start + direction * step + props.options.length) % props.options.length
    if (enabled(index)) { setActive(index); return }
  }
}
function position() {
  if (!trigger.value || !panel.value) return
  const rect = trigger.value.getBoundingClientRect()
  const viewport = window.visualViewport
  const leftEdge = (viewport?.offsetLeft ?? 0) + 8
  const topEdge = (viewport?.offsetTop ?? 0) + 8
  const rightEdge = leftEdge + (viewport?.width ?? window.innerWidth) - 16
  const bottomEdge = topEdge + (viewport?.height ?? window.innerHeight) - 16
  const gap = 6
  const below = bottomEdge - rect.bottom - gap
  const above = rect.top - topEdge - gap
  const upwards = below < 220 && above > below
  const maxHeight = Math.max(0, Math.min(300, upwards ? above : below))
  const width = Math.min(Math.max(rect.width, 220), rightEdge - leftEdge)
  const left = Math.max(leftEdge, Math.min(rect.left, rightEdge - width))
  const height = Math.min(panel.value.scrollHeight + 2, maxHeight)
  placement.value = { left: `${left}px`, top: `${Math.max(topEdge, upwards ? rect.top - gap - height : rect.bottom + gap)}px`, width: `${width}px`, maxHeight: `${maxHeight}px` }
}
function outside(event: Event) {
  const path = event.composedPath()
  if (!path.includes(trigger.value!) && !path.includes(panel.value!)) close(false)
}
function scrolled(event: Event) {
  if (!observingScroll) return
  if (event.target instanceof Node && panel.value?.contains(event.target)) return
  // 输入法收起或弹出时浏览器会滚动页面，跟随触发按钮重新定位即可
  if (keyboardShift()) { position(); return }
  close(false)
}
let viewHeight = 0
function keyboardShift() { return !!window.visualViewport && Math.abs(window.visualViewport.height - viewHeight) > 80 }
function focusChanged(event: Event) {
  if (event.target !== trigger.value && !(event.target instanceof Node && panel.value?.contains(event.target))) close(false)
}
function listen(add: boolean) {
  const action = add ? 'addEventListener' : 'removeEventListener'
  document[action]('pointerdown', outside, true)
  document[action]('focusin', focusChanged, true)
  window[action]('scroll', scrolled, true)
  window[action]('resize', resized)
  window.visualViewport?.[action]('resize', resized)
  window.visualViewport?.[action]('scroll', resized)
}
// 视口变化（手机输入法、旋转屏幕）只重新定位，不关闭
function resized() { if (opened.value) position() }
async function open(edge?: 'first' | 'last') {
  if (props.disabled || opened.value) return
  const current = ++openSequence
  // A popover in the dialog DOM stays interactive in its modal focus scope.
  // Its top-layer presentation escapes the dialog's overflow and viewport edges.
  teleportTarget.value = trigger.value?.closest('dialog') ?? 'body'
  opened.value = true
  const index = props.options.findIndex(option => option.value === props.modelValue && !option.disabled)
  active.value = edge ? boundary(edge === 'last') : index >= 0 ? index : boundary()
  await nextTick()
  if (!opened.value || current !== openSequence) return
  panel.value?.showPopover()
  position()
  viewHeight = window.visualViewport?.height ?? 0
  observingScroll = false
  listen(true)
  reveal()
  // Drain the scroll generated by trigger focus; pointer and keyboard handling stay immediate.
  requestAnimationFrame(() => {
    if (opened.value && current === openSequence) { position(); observingScroll = true }
  })
}
function close(restore = true) {
  if (!opened.value) return
  ++openSequence
  panel.value?.hidePopover()
  opened.value = false
  listen(false)
  clearTimeout(searchTimer)
  search = ''
  if (restore && trigger.value?.isConnected && !props.disabled) trigger.value.focus({ preventScroll: true })
}
function choose(index: number) {
  if (!enabled(index)) return
  emit('update:modelValue', props.options[index]!.value)
  close()
}
function typeahead(key: string) {
  clearTimeout(searchTimer)
  search += key.toLocaleLowerCase()
  const repeated = [...search].every(char => char === search[0])
  const prefix = repeated ? search[0]! : search
  const start = repeated || search.length === 1 ? active.value + 1 : active.value
  for (let step = 0; step < props.options.length; step++) {
    const index = (Math.max(0, start) + step) % props.options.length
    if (enabled(index) && props.options[index]!.label.toLocaleLowerCase().startsWith(prefix)) { setActive(index); break }
  }
  searchTimer = setTimeout(() => { search = '' }, 650)
}
function keydown(event: KeyboardEvent) {
  if (props.disabled || event.isComposing || event.ctrlKey || event.metaKey || event.altKey) return
  if (event.key === 'Tab') { close(false); return }
  if (event.key === 'Escape') {
    if (opened.value) { event.preventDefault(); event.stopPropagation(); close() }
    return
  }
  if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', ' '].includes(event.key)) {
    event.preventDefault()
    if (!opened.value) { void open(event.key === 'Home' ? 'first' : event.key === 'End' ? 'last' : undefined); return }
    if (event.key === 'ArrowDown') move(1)
    else if (event.key === 'ArrowUp') move(-1)
    else if (event.key === 'Home' || event.key === 'End') setActive(boundary(event.key === 'End'))
    else choose(active.value)
  } else if (event.key.length === 1) {
    event.preventDefault()
    if (!opened.value) void open()
    typeahead(event.key)
  }
}
watch(() => props.disabled, disabled => { if (disabled) close(false) })
watch(() => props.options, () => {
  if (opened.value) { if (!enabled(active.value)) setActive(boundary()); void nextTick(position) }
})
onBeforeUnmount(() => { close(false); clearTimeout(searchTimer) })
</script>

<template>
  <div class="custom-select">
    <button :id="`${id}-trigger`" ref="trigger" type="button" role="combobox" class="custom-select-trigger"
      :disabled="disabled" :aria-label="label" aria-haspopup="listbox" :aria-expanded="opened"
      :aria-controls="`${id}-listbox`" :aria-activedescendant="activeId" aria-autocomplete="none"
      @click="opened ? close() : open()" @keydown="keydown">
      <span class="custom-select-value">{{ selected?.label ?? placeholder }}</span>
      <svg class="custom-select-chevron" viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path d="m4 6 4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
    </button>
    <Teleport :to="teleportTarget">
      <div v-if="opened" :id="`${id}-listbox`" ref="panel" class="custom-select-panel" popover="manual" role="listbox" :aria-label="label" :style="placement">
        <div v-for="(option, index) in options" :id="`${id}-option-${index}`" :key="option.value" role="option"
          :data-index="index" :data-value="option.value" :aria-selected="option.value === modelValue" :aria-disabled="option.disabled || undefined"
          :class="['custom-select-option', { 'is-active': index === active, 'is-selected': option.value === modelValue }]"
          @pointerdown.prevent @pointermove="event => event.pointerType !== 'touch' && enabled(index) && (active = index)" @click="choose(index)">
          <span>{{ option.label }}</span><span class="custom-select-check" aria-hidden="true">{{ option.value === modelValue ? '✓' : '' }}</span>
        </div>
        <div v-if="!options.length" class="custom-select-empty" role="status">{{ emptyText }}</div>
      </div>
    </Teleport>
  </div>
</template>
