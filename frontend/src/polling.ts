import { onBeforeUnmount, onMounted } from 'vue'

// Schedule after completion so slow responses never accumulate polling requests.
export function usePolling(task: () => Promise<void>, interval = 5000, immediate = true) {
  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = false
  let running = false
  async function tick() {
    if (running) return
    running = true
    // 标签页在后台时不请求，回到前台立即补一次
    try { if (!document.hidden) await task() } finally {
      running = false
      if (!stopped) { clearTimeout(timer); timer = setTimeout(() => void tick(), interval) }
    }
  }
  function visible() { if (!document.hidden && !stopped) { clearTimeout(timer); void tick() } }
  onMounted(() => {
    document.addEventListener('visibilitychange', visible)
    if (immediate) void tick(); else timer = setTimeout(() => void tick(), interval)
  })
  onBeforeUnmount(() => { stopped = true; clearTimeout(timer); document.removeEventListener('visibilitychange', visible) })
}
