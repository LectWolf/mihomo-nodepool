<script setup lang="ts">
defineProps<{ name: string }>()
const paths: Record<string, string> = {
  nodes: 'M4 4h6v6H4z M14 4h6v6h-6z M4 14h6v6H4z M14 14h6v6h-6z',
  sources: 'M5 4h14v16H5z M8 8h8 M8 12h8 M8 16h5',
  settings: 'M4 7h16 M4 17h16 M8 4v6 M16 14v6',
  search: 'M10.5 17a6.5 6.5 0 1 0 0-13 6.5 6.5 0 0 0 0 13 M16 16l5 5',
  probe: 'M3 12h4l3-7 4 14 3-7h4',
  check: 'M5 12l4 4L19 6',
  alert: 'M12 4 2 21h20L12 4z M12 9v5 M12 17v1',
  clock: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18 M12 7v5l3 2',
  refresh: 'M20 7v5h-5 M4 17v-5h5 M5 8a8 8 0 0 1 13-3l2 3 M4 16l2 3a8 8 0 0 0 13-3',
  edit: 'M15 5l4 4 M4 20l5-1L20 8a3 3 0 0 0-4-4L5 15l-1 5z',
  trash: 'M3 6h18 M9 6V3h6v3 M6 6l1 15h10l1-15 M10 10v7 M14 10v7',
  plus: 'M12 5v14 M5 12h14',
  close: 'M6 6l12 12 M6 18 18 6',
  left: 'M15 5l-7 7 7 7',
  right: 'M9 5l7 7-7 7',
  down: 'M6 9l6 6 6-6',
  external: 'M14 3h7v7 M21 3 11 13 M10 5H4v15h15v-6',
  version: 'M12 3 3 7.5v9l9 4.5 9-4.5v-9L12 3z M3 7.5l9 5 9-5 M12 12.5V21',
  link: 'M10 14l4-4 M8 16l-2 2a4 4 0 0 1-6-6l5-5a4 4 0 0 1 6 0 M16 8l2-2a4 4 0 0 1 6 6l-5 5a4 4 0 0 1-6 0',
  upload: 'M12 16V3 M7 8l5-5 5 5 M4 15v6h16v-6',
  logout: 'M9 4H4v16h5 M10 12h11 M17 8l4 4-4 4',
  info: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18 M12 11v6 M12 7v1',
}
</script>

<template>
  <svg class="app-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path :d="paths[name] ?? paths.info" /></svg>
</template>
