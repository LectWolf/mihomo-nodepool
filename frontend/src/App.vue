<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  CORE_NOT_INTEGRATED,
  SYSTEM_ENDPOINT,
  fetchSystemInfo,
  isAbortError,
  type SystemInfo,
} from './api/system'

const sections = [
  { id: 'status', label: '系统状态' },
  { id: 'scope', label: '实现范围' },
  { id: 'deploy', label: '部署信息' },
] as const

type SectionId = (typeof sections)[number]['id']

const activeSection = ref<SectionId>('status')
const loading = ref(false)
const system = ref<SystemInfo | null>(null)
const errorMessage = ref('')
const fetchedAt = ref<Date | null>(null)
let controller: AbortController | null = null

const hasSnapshot = computed(() => system.value !== null)

const scopeItems = [
  {
    title: '控制台骨架',
    status: '本页已提供',
    live: true,
    detail: '浅色中文页面、侧栏锚点，以及这一页系统准备信息。',
  },
  {
    title: '系统状态接口',
    status: '本页会真实请求',
    live: true,
    detail: '只请求 GET /api/v1/system。失败时显示错误，不填入示例版本。',
  },
  {
    title: '外部 MySQL',
    status: '只展示状态',
    live: false,
    detail: '是否已配置、是否已连接由后端返回。本页不收集、不展示地址、账号或密码。',
  },
  {
    title: 'Go 后端与 Docker',
    status: '形态已确认，前端目录未包含',
    live: false,
    detail: '第一版形态是网页加 Docker，后端为 Go。本前端目录没有后端代码或容器定义。',
  },
  {
    title: 'Mihomo 内核',
    status: '尚未接入',
    live: false,
    detail: '约定的 core.state 为 not_integrated。没有内核启停、配置或日志。',
  },
  {
    title: '节点管理',
    status: '尚未实现',
    live: false,
    detail: '没有节点列表、订阅、测速、选择或数量统计。',
  },
  {
    title: '版本更新',
    status: '尚未实现',
    live: false,
    detail: '版本、提交和构建时间只展示接口原文。没有检查更新或升级操作。',
  },
] as const

function isSectionId(value: string): value is SectionId {
  return sections.some((section) => section.id === value)
}

function syncHash(): void {
  const id = location.hash.replace('#', '')
  if (isSectionId(id)) {
    activeSection.value = id
  }
}

function selectSection(id: SectionId): void {
  activeSection.value = id
}

async function refresh(): Promise<void> {
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  errorMessage.value = ''

  try {
    const info = await fetchSystemInfo(current.signal)
    if (controller !== current) {
      return
    }
    system.value = info
    fetchedAt.value = new Date()
  } catch (error) {
    if (controller !== current || isAbortError(error)) {
      return
    }
    errorMessage.value = error instanceof Error ? error.message : '读取系统状态失败'
  } finally {
    if (controller === current) {
      loading.value = false
    }
  }
}

function formatFetchedAt(date: Date): string {
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hourCycle: 'h23',
  }).format(date)
}

function configuredLabel(value: boolean): string {
  return value ? '已配置' : '未配置'
}

function connectedLabel(value: boolean): string {
  return value ? '已连接' : '未连接'
}

onMounted(() => {
  syncHash()
  window.addEventListener('hashchange', syncHash)
  void refresh()
})

onUnmounted(() => {
  controller?.abort()
  window.removeEventListener('hashchange', syncHash)
})
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <div class="brand">
        <span class="mark" aria-hidden="true">
          <svg viewBox="0 0 32 32">
            <rect width="32" height="32" rx="8" fill="currentColor" />
            <circle cx="16" cy="16" r="6" fill="none" stroke="#fff" stroke-width="2" />
            <circle cx="16" cy="16" r="2" fill="#fff" />
          </svg>
        </span>
        <div>
          <p class="brand-name">Mihomo NodePool</p>
          <p class="brand-phase">系统准备</p>
        </div>
      </div>
      <nav class="nav" aria-label="页面分区">
        <a
          v-for="section in sections"
          :key="section.id"
          :href="`#${section.id}`"
          :aria-current="activeSection === section.id ? 'page' : undefined"
          @click="selectSection(section.id)"
        >
          {{ section.label }}
        </a>
      </nav>
      <p class="sidebar-note">当前没有节点、内核或更新操作。</p>
    </aside>

    <main class="main">
      <header class="page-head">
        <div>
          <p class="eyebrow">准备阶段</p>
          <h1>系统准备</h1>
          <p class="lead">
            这是第一版控制台骨架。它只读取后端系统状态，不管理节点，也不执行更新。未连接时不显示占位版本或假统计。
          </p>
        </div>
      </header>

      <section id="status" class="panel">
        <div class="panel-head">
          <div>
            <h2>系统状态</h2>
            <p class="sub">数据只来自 {{ SYSTEM_ENDPOINT }}。刷新会重新请求，不会使用本地示例。</p>
          </div>
          <button class="refresh" type="button" :aria-busy="loading" @click="refresh">
            <span v-if="loading" class="spinner" aria-hidden="true"></span>
            {{ loading ? '正在刷新' : '刷新状态' }}
          </button>
        </div>

        <div v-if="loading && !hasSnapshot" class="banner banner-wait" role="status">
          <p>正在读取系统状态…</p>
        </div>
        <div v-else-if="loading && hasSnapshot" class="banner banner-wait" role="status">
          <p>正在刷新。下面仍是上次成功读取的结果，完成后会替换。</p>
        </div>
        <div v-if="errorMessage" class="banner banner-error" role="alert">
          <p>{{ errorMessage }}</p>
          <p v-if="hasSnapshot">上次成功读取仍显示在下方，它不是本次刷新的结果。</p>
          <p v-else>版本、数据库和内核字段未显示，因为这次没有拿到有效响应。</p>
        </div>

        <div class="grid">
          <article class="card">
            <h3>连接</h3>
            <dl class="fields">
              <div>
                <dt>方法</dt>
                <dd>GET</dd>
              </div>
              <div>
                <dt>路径</dt>
                <dd><code>{{ SYSTEM_ENDPOINT }}</code></dd>
              </div>
              <div>
                <dt>读取时间</dt>
                <dd>{{ fetchedAt ? formatFetchedAt(fetchedAt) : '尚未成功读取' }}</dd>
              </div>
            </dl>
          </article>

          <article class="card">
            <h3>接口返回</h3>
            <p v-if="!system" class="sub">等待一次成功响应后，这里显示名称、版本、提交和构建时间原文。</p>
            <dl v-else class="fields">
              <div>
                <dt>名称</dt>
                <dd>{{ system.name }}</dd>
              </div>
              <div>
                <dt>版本</dt>
                <dd>{{ system.version }}</dd>
              </div>
              <div>
                <dt>提交</dt>
                <dd><code>{{ system.commit }}</code></dd>
              </div>
              <div>
                <dt>构建时间</dt>
                <dd><code>{{ system.build_time }}</code></dd>
              </div>
            </dl>
          </article>

          <article class="card">
            <h3>数据库</h3>
            <p v-if="!system" class="sub">未连接，不显示配置或连接状态。</p>
            <template v-else>
              <div class="chips">
                <span class="chip">{{ configuredLabel(system.database.configured) }}</span>
                <span class="chip" :class="system.database.connected ? 'chip-live' : 'chip-wait'">
                  {{ connectedLabel(system.database.connected) }}
                </span>
              </div>
            </template>
            <p class="sub">只展示 configured 与 connected。不提供凭据输入，也不回显地址、账号或密码。</p>
          </article>

          <article class="card">
            <h3>内核与节点</h3>
            <p v-if="!system" class="sub">内核状态尚未从接口确认。</p>
            <template v-else>
              <div class="chips">
                <span class="chip">内核尚未接入</span>
                <span class="chip"><code>{{ system.core.state }}</code></span>
              </div>
            </template>
            <p class="sub">
              节点模块尚未接入。接口没有节点字段，本页不构造节点数量、延迟或健康统计。约定状态为
              {{ CORE_NOT_INTEGRATED }}。
            </p>
          </article>
        </div>
      </section>

      <section id="scope" class="panel">
        <div class="panel-head">
          <div>
            <h2>实现范围</h2>
            <p class="sub">下列是第一版已确认的边界。标签只说明本页是否提供，不表示后端或节点已经完成。</p>
          </div>
        </div>
        <div class="scope">
          <article v-for="item in scopeItems" :key="item.title">
            <h3>{{ item.title }}</h3>
            <span class="chip" :class="item.live ? 'chip-live' : undefined">{{ item.status }}</span>
            <p>{{ item.detail }}</p>
          </article>
        </div>
      </section>

      <section id="deploy" class="panel">
        <div class="panel-head">
          <div>
            <h2>部署信息</h2>
            <p class="sub">这里记录当前骨架的连接方式，不是运行状况，也不是已完成的部署。</p>
          </div>
        </div>
        <dl class="facts">
          <div>
            <dt>开发监听</dt>
            <dd><code>127.0.0.1:5173</code>。端口被占用时不会自动改到其他端口。</dd>
          </div>
          <div>
            <dt>开发代理</dt>
            <dd>
              <code>/api</code> 与 <code>/healthz</code> 转发到 <code>http://127.0.0.1:8080</code>。页面本身请求相对路径。
            </dd>
          </div>
          <div>
            <dt>健康检查</dt>
            <dd>代理已配置 <code>/healthz</code>。本页不请求它，也不假设它返回健康。</dd>
          </div>
          <div>
            <dt>后端与数据</dt>
            <dd>Go 后端连接外部 MySQL。数据库凭据不进入本前端。</dd>
          </div>
          <div>
            <dt>容器</dt>
            <dd>网页加 Docker 是已确认形态。本前端目录没有 Dockerfile、compose 或可运行容器。</dd>
          </div>
          <div>
            <dt>构建环境</dt>
            <dd>本机 Node.js 24.17。页面使用系统字体，不请求外部字体或图像。</dd>
          </div>
        </dl>
      </section>
    </main>
  </div>
</template>
