<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import '../community.css'
const verge = 'https://github.com/clash-verge-rev/clash-verge-rev'
const clients = [
  { platform: 'iOS', name: 'Stash', note: '付费 · 支持 Mihomo / Clash 风格配置', url: 'https://stash.ws' },
  { platform: '安卓', name: 'ClashMetaForAndroid', note: 'Mihomo 内核', url: 'https://github.com/MetaCubeX/ClashMetaForAndroid' },
  { platform: 'macOS', name: 'Clash Verge Rev', note: 'Mihomo 完整配置', url: verge },
  { platform: 'Windows', name: 'Clash Verge Rev', note: 'Mihomo 完整配置', url: verge },
  { platform: 'Linux', name: 'Clash Verge Rev', note: 'Mihomo 完整配置', url: verge },
  { platform: 'OpenWrt', name: 'OpenClash', note: '路由器插件', url: 'https://github.com/vernesong/OpenClash' },
]
</script>
<template>
  <section class="panel client-recommendations">
    <div class="panel-heading"><div><h2>推荐客户端</h2><p class="small muted">优先使用 Clash / Mihomo 完整配置，保留节点参数与规则。以下入口均为项目官网或官方仓库。</p></div></div>
    <div class="client-grid"><a v-for="client in clients" :key="client.platform" :href="client.url" target="_blank" rel="noopener noreferrer"><span class="small muted">{{ client.platform }}</span><strong>{{ client.name }}<AppIcon name="external" /></strong><small>{{ client.note }}</small></a></div>
    <p class="small muted">URI 客户端可选：<a href="https://github.com/2dust/v2rayN" target="_blank" rel="noopener noreferrer">v2rayN（Windows / macOS / Linux）</a>、<a href="https://github.com/2dust/v2rayNG" target="_blank" rel="noopener noreferrer">v2rayNG（安卓）</a>。v2ray URI 不含规则，部分协议和参数无法保真导出，请查看节点兼容性提示。</p>
  </section>
</template>
