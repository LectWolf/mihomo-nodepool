import { onBeforeUnmount, onMounted } from 'vue'

// Schedule after completion so slow responses never accumulate polling requests.
export function usePolling(task: () => Promise<void>, interval = 5000, immediate = true) {
  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = false
  async function tick() {
    try { await task() } finally {
      if (!stopped) timer = setTimeout(() => void tick(), interval)
    }
  }
  onMounted(() => { if (immediate) void tick(); else timer = setTimeout(() => void tick(), interval) })
  onBeforeUnmount(() => { stopped = true; clearTimeout(timer) })
}
