<script setup lang="ts">
import { computed, ref } from 'vue'
export interface Bar { label: string; value: number; detail?: string; missing?: boolean }
const props = withDefaults(defineProps<{ bars: Bar[]; format?: (v: number) => string; tone?: string; height?: number; every?: number; empty?: string; bytes?: boolean; aggregate?: 'sum' | 'avg' }>(), { format: (v: number) => String(v), tone: 'green', height: 160, every: 1, empty: '暂无数据' })
const active = ref<number | null>(null)
const max = computed(() => Math.max(0, ...props.bars.map(b => b.value)))
// 取一个整齐的刻度上限，避免最高柱贴顶
const ceiling = computed(() => {
  if (max.value <= 0) return 1
  // 字节按 1024 进制取整，刻度显示为整齐的 GB/TB
  const base = props.bytes ? 1024 ** Math.floor(Math.log(max.value) / Math.log(1024)) : 1
  const v = max.value / base, p = 10 ** Math.floor(Math.log10(v)), n = v / p
  return (n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10) * p * base
})
// 平均值只统计有数据的柱，来源上架前的空白不拉低结果
const total = computed(() => { const list = props.aggregate === 'avg' ? props.bars.filter(b => !b.missing) : props.bars; const sum = list.reduce((n, b) => n + b.value, 0); return props.aggregate === 'avg' ? (list.length ? sum / list.length : 0) : sum })
const shown = computed(() => active.value === null ? null : props.bars[active.value])
</script>

<template>
  <div class="bar-chart" :class="`tone-${tone}`">
    <div class="bar-chart-head"><span class="small muted">{{ shown ? shown.detail || shown.label : aggregate === 'avg' ? '平均' : '合计' }}</span><strong>{{ shown?.missing ? '无数据' : format(shown ? shown.value : total) }}</strong></div>
    <div class="bar-chart-body" :style="{ height: `${height}px` }" @mouseleave="active = null">
      <div class="bar-grid" aria-hidden="true"><span>{{ format(ceiling) }}</span><span>{{ format(ceiling / 2) }}</span><span>0</span></div>
      <div v-if="!max" class="bar-empty small muted">{{ empty }}</div>
      <div class="bar-track" role="list">
        <button v-for="(bar, index) in bars" :key="index" type="button" role="listitem" class="bar" :class="{ active: active === index, missing: bar.missing }" :aria-label="`${bar.detail || bar.label}：${bar.missing ? '无数据' : format(bar.value)}`" @mouseenter="active = index" @focus="active = index" @blur="active = null">
          <span :style="{ height: `${bar.value > 0 ? Math.max(2, (bar.value / ceiling) * 100) : 0}%` }"></span>
        </button>
      </div>
    </div>
    <div class="bar-axis" aria-hidden="true"><span v-for="(bar, index) in bars" :key="index">{{ index % every === 0 || index === bars.length - 1 ? bar.label : '' }}</span></div>
  </div>
</template>
