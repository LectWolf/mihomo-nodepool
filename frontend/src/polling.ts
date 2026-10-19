import { onBeforeUnmount, onMounted } from 'vue'

// Schedule after completion so slow responses never accumulate polling requests.
export function usePolling(task: () => Promise<void>, interval: number | (() => number) = 5000, immediate = true) {
  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = false
  let running = false
  const nextInterval = () => typeof interval === 'function' ? interval() : interval
  async function tick(force = false) {
    if (running) return
    running = true
    // 标签页在后台时跳过定时请求，回到前台立即补一次；首次加载不跳过
    try { if (force || !document.hidden) await task() } finally {
      running = false
      if (!stopped) { clearTimeout(timer); timer = setTimeout(() => void tick(), nextInterval()) }
    }
  }
  function visible() { if (!document.hidden && !stopped) { clearTimeout(timer); void tick() } }
  onMounted(() => {
    document.addEventListener('visibilitychange', visible)
    if (immediate) void tick(true); else timer = setTimeout(() => void tick(), nextInterval())
  })
  onBeforeUnmount(() => { stopped = true; clearTimeout(timer); document.removeEventListener('visibilitychange', visible) })
}
