<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import '../community.css'
const clients = [
  { platform: 'iPhone · iPad · Mac · Apple TV', name: 'Clash Apple 原生客户端', note: '基于 Mihomo，导入完整 Clash 配置。中国大陆 / 俄罗斯区 Apple ID 需切换地区后下载。', url: 'https://clash.md/zh/', icon: 'A' },
  { platform: 'Windows · Linux · 安卓 · macOS', name: 'FlClash', note: '跨平台开源 Mihomo 客户端，界面简洁，支持订阅自动更新。', url: 'https://github.com/chen08209/FlClash', icon: 'F' },
  { platform: 'OpenWrt 路由器', name: 'OpenClash', note: '路由器插件，全家设备统一走代理。', url: 'https://github.com/vernesong/OpenClash', icon: 'O' },
]
</script>
<template>
  <section class="panel client-recommendations">
    <div class="panel-heading"><div><h2>推荐客户端</h2><p class="small muted">优先导入 Clash / Mihomo 完整配置，保留节点参数与规则分流。</p></div></div>
    <div class="client-row"><a v-for="client in clients" :key="client.name" class="client-card" :href="client.url" target="_blank" rel="noopener noreferrer"><span class="client-mark" aria-hidden="true">{{ client.icon }}</span><span class="client-copy"><small>{{ client.platform }}</small><strong>{{ client.name }}<AppIcon name="external" /></strong><span>{{ client.note }}</span></span></a></div>
    <p class="small muted">只支持 URI 的客户端可用 v2ray 格式，但不含规则，部分协议参数无法保真导出，请查看节点兼容性提示。</p>
  </section>
</template>
