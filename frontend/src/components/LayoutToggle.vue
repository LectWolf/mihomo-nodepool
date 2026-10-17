<script setup lang="ts">
import { classic, setLayout } from '../layout'
import { navigate, route } from '../router'
import AppIcon from './AppIcon.vue'
// 切换布局时把来源目录与“按来源”视图互相对应
function toggle() {
  const r = route.value, query = new URLSearchParams(r.query)
  if (classic.value) {
    setLayout('modern')
    if (r.section === 'catalog') navigate('nodes', new URLSearchParams({ by: 'sources' }), true)
  } else {
    setLayout('classic')
    if (r.section === 'nodes' && (!r.query || (query.get('by') === 'sources' && query.size === 1))) navigate('catalog', undefined, true)
  }
}
</script>
<template>
  <button type="button" class="layout-toggle compact" :title="classic ? '切换到新版界面' : '切换到经典版界面'" :aria-label="classic ? '切换到新版界面' : '切换到经典版界面'" @click="toggle"><AppIcon :name="classic ? 'layout-side' : 'layout-top'" /><span>{{ classic ? '新版' : '经典版' }}</span></button>
</template>
