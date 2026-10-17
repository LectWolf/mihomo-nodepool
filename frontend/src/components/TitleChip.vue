<script setup lang="ts">
import { onMounted } from 'vue'
import { loadTitles, titleColor } from '../titles'
const props = defineProps<{ name?: string | null; large?: boolean; color?: string }>()
onMounted(() => { if (!props.color) void loadTitles() })
</script>

<template>
  <span v-if="name" :class="['title-chip', `title-${color || titleColor(name)}`, { large }]">{{ name }}</span>
</template>
