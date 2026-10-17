<script setup lang="ts">
import { computed } from 'vue'
import type { NodeInsights, SubscriptionUsage } from '../community'
import UsageHint from './UsageHint.vue'
import AppIcon from './AppIcon.vue'
import '../community.css'
const props = defineProps<{ insights?: NodeInsights; minLevel?: number; usage?: SubscriptionUsage | null; detail?: boolean }>()
const warnings = computed(() => props.insights?.warnings ?? [])
const tlsLabels = { enabled: '已启用', disabled: '未启用', not_applicable: '不适用', unknown: '未知' }
</script>
<template>
  <span v-if="!detail" class="node-insights">
    <span v-if="minLevel" class="insight-tag" :title="`来源最低等级 Lv${minLevel}`">Lv{{ minLevel }}+</span>
    <span v-if="insights && !insights.v2ray_compatible" class="insight-tag insight-icon insight-risk" role="img" aria-label="URI 不可导" :title="`URI 不可导：${insights.v2ray_reason || '无法以 URI 保真导出，请使用 Clash / Mihomo 完整配置'}`"><AppIcon name="unlink" /></span>
    <span v-for="warning in warnings" :key="warning.code" class="insight-tag insight-icon" :class="{ 'insight-risk': warning.severity !== 'info' }" role="img" :aria-label="warning.message" :title="warning.message"><AppIcon :name="warning.code === 'certificate_verification_disabled' ? 'shield' : warning.code === 'unencrypted_transport' ? 'unlock' : warning.severity === 'info' ? 'info' : 'alert'" /></span>
    <UsageHint :usage="usage" />
  </span>
  <section v-else class="node-insights-detail">
    <h3>兼容性与配置提示</h3>
    <p class="small muted">以下为静态配置分析，不是安全审计结论，也不代表客户端实测结果。Mihomo 完整配置优先，URI 不保证全部参数兼容。</p>
    <dl v-if="insights" class="detail-list">
      <div><dt>v2ray URI</dt><dd>{{ insights.v2ray_compatible ? '可导出，仍需确认客户端支持' : '无法保真导出' }}<small v-if="insights.v2ray_reason" class="cell-sub">{{ insights.v2ray_reason }}</small></dd></div>
      <div v-if="insights.cipher"><dt>加密方式</dt><dd>{{ insights.cipher }}</dd></div>
      <div v-if="insights.transport"><dt>传输方式</dt><dd>{{ insights.transport }}</dd></div>
      <div><dt>TLS</dt><dd>{{ tlsLabels[insights.tls] ?? '未知' }}</dd></div>
      <div v-if="minLevel !== undefined"><dt>来源门槛</dt><dd>{{ minLevel ? `Lv${minLevel} 及以上` : 'Lv0 不限' }}</dd></div>
    </dl>
    <p v-else class="small muted">暂未提供配置分析。</p>
    <ul v-if="warnings.length" class="insight-warnings"><li v-for="warning in warnings" :key="warning.code" :class="{ 'danger-text': warning.severity !== 'info' }" :title="warning.message">{{ warning.message }}</li></ul>
    <UsageHint v-if="usage" :usage="usage" detail />
  </section>
</template>
