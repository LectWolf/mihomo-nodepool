<script setup lang="ts">
import { computed, ref } from 'vue'
import { errorText, formatDate, isCancelled, json, safeReleaseUrl, useApi, type Releases } from '../api/system'
import { usePolling } from '../polling'
import Modal from './Modal.vue'
import CustomSelect from './CustomSelect.vue'
const emit = defineEmits<{ close: []; restart: [] }>()
const api = useApi()
const data = ref<Releases | null>(null)
const loading = ref(false)
const loadError = ref('')
const actionError = ref('')
const notice = ref('')
const selected = ref('')
const submitting = ref(false)
const waitingForTask = ref(false)
const confirm = ref<'update' | 'history' | 'backup' | 'restart' | null>(null)
let submittedAt = 0
let initialState = ''
const activeStates = ['checking', 'downloading', 'installing', 'restarting']
const running = computed(() => !!data.value && activeStates.includes(data.value.state))
const busy = computed(() => submitting.value || waitingForTask.value || running.value)
const selectedRelease = computed(() => data.value?.releases.find(release => release.version === selected.value))
const releaseOptions = computed(() => data.value?.releases.map(release => ({ value: release.version, label: `${release.version}${release.version === data.value?.latest_version ? ' · 最新稳定版' : ''}${release.version === data.value?.current_version ? ' · 当前版本' : ''}` })) ?? [])
const stateLabels = { idle: '待命', checking: '正在检查', downloading: '正在下载', installing: '正在安装', restarting: '正在重启', done: '操作完成', error: '操作失败' }
const canInstall = computed(() => !!selectedRelease.value && !!data.value?.can_update && selected.value !== data.value.current_version && !data.value.need_restart && !busy.value)
async function load() {
  if (loading.value) return
  loading.value = true
  try {
    const value = await api<Releases>('/releases')
    data.value = value; loadError.value = ''
    if (!selected.value) selected.value = value.releases.find(release => release.version === value.latest_version)?.version ?? value.releases[0]?.version ?? ''
    if (waitingForTask.value) {
      if (activeStates.includes(value.state) || ((value.state === 'done' || value.state === 'error') && `${value.state}:${value.message}:${value.need_restart}` !== initialState)) waitingForTask.value = false
      else if (Date.now() - submittedAt > 120000) {
        waitingForTask.value = false
        actionError.value = '暂未确认任务状态，请重新检查版本状态后再操作。'
      }
    }
  } catch (error) { if (!isCancelled(error)) loadError.value = errorText(error) }
  finally { loading.value = false }
}
usePolling(load, 2500)
async function execute() {
  const action = confirm.value
  if (!action || submitting.value || busy.value) return
  submitting.value = true; actionError.value = ''; notice.value = ''
  try {
    if (action === 'restart') {
      await api('/system/restart', json('POST'))
      confirm.value = null
      emit('restart')
    } else {
      const path = action === 'update' ? '/system/update' : '/system/rollback'
      const payload = action === 'backup' ? {} : { version: selected.value }
      initialState = `${data.value?.state}:${data.value?.message}:${data.value?.need_restart}`
      await api(path, json('POST', payload))
      submittedAt = Date.now()
      waitingForTask.value = true
      notice.value = '任务已提交，正在跟踪执行进度。'
      confirm.value = null
      await load()
    }
  } catch (error) { if (!isCancelled(error)) { actionError.value = errorText(error); confirm.value = null } }
  finally { submitting.value = false }
}
const confirmationText = computed(() => {
  if (confirm.value === 'restart') return '立即重启服务？连接会短暂中断，控制台会自动尝试重新连接。'
  if (confirm.value === 'backup') return '恢复上一次保存在本地的程序备份？完成后需要重启服务。回退仅切换程序文件，数据库保持当前内容。'
  return `确定${confirm.value === 'history' ? '回退' : '安装'}到 ${selected.value}？完成后需要重启服务，数据库保持当前内容。`
})
</script>

<template>
  <Modal title="版本与发布" wide :busy="submitting" @close="emit('close')">
    <div v-if="!data && loading" class="empty-state" role="status"><span class="spinner"></span><h3>正在获取真实发布版本</h3></div>
    <div v-if="loadError" class="notice error" role="alert">{{ loadError }}<button :disabled="loading" @click="load">重新检查</button></div>
    <template v-if="data">
      <div class="version-summary"><div><p class="small muted">当前版本</p><strong>{{ data.current_version || '未提供' }}</strong></div><div><p class="small muted">最新稳定版</p><strong>{{ data.latest_version || '暂无发布' }}</strong></div><span :class="['badge', data.update_available ? 'queued' : 'ready']">{{ data.update_available ? '有可用更新' : '暂无可用更新' }}</span></div>
      <div v-if="data.need_restart" class="notice warning" role="status">程序文件已准备好，请立即重启以使用所选版本。</div>
      <div v-if="running || waitingForTask || data.state === 'error' || data.message" :class="['notice', data.state === 'error' ? 'error' : 'info']" role="status"><span v-if="running || waitingForTask" class="spinner"></span><strong>{{ waitingForTask ? '等待任务开始' : stateLabels[data.state] }}</strong><span>{{ data.message }}</span></div>
      <p v-if="notice && !running && !data.need_restart && data.state !== 'error'" class="small green-text" role="status">{{ notice }}</p>
      <div class="release-toolbar"><div class="release-select-field"><span class="field-label">选择发布版本</span><CustomSelect v-model="selected" label="选择发布版本" :disabled="busy || !data.releases.length" :options="releaseOptions" placeholder="暂无可选版本" /></div><button :disabled="loading || busy" @click="load">{{ loading ? '检查中…' : '↻ 检查版本' }}</button></div>
      <article v-if="selectedRelease" class="release-notes"><div class="panel-heading"><div><h3>{{ selectedRelease.name || selectedRelease.version }}</h3><p class="small muted">{{ formatDate(selectedRelease.published_at) }}</p></div><a v-if="safeReleaseUrl(selectedRelease.url)" :href="safeReleaseUrl(selectedRelease.url)" target="_blank" rel="noopener noreferrer" class="small green-text">查看原文 ↗</a></div><div class="release-body">{{ selectedRelease.body || '此版本未提供发布说明。' }}</div></article>
      <div v-else class="empty-state"><span class="empty-icon">◷</span><h3>暂无发布记录</h3><p>获取到的发布版本将在这里显示。</p></div>
      <div v-if="!data.can_update" class="notice warning">{{ data.message || '当前运行环境不支持在线更新。' }}</div>
      <div class="release-actions"><button class="primary" :disabled="!canInstall" @click="confirm = 'update'">安装所选版本</button><button :disabled="!canInstall || selected === data.latest_version" @click="confirm = 'history'">回退到所选历史版</button></div>
      <div class="version-maintenance"><div><strong>恢复本地备份</strong><p class="small muted">恢复上一次备份的程序文件，随后重启生效。</p></div><button :disabled="!data.can_rollback || busy || data.need_restart" @click="confirm = 'backup'">恢复本地 backup</button></div>
      <div class="version-maintenance"><div><strong>重启服务</strong><p class="small muted">重启期间连接会短暂中断，恢复后自动重连。</p></div><button :class="{ primary: data.need_restart }" :disabled="busy" @click="confirm = 'restart'">立即重启</button></div>
      <p class="small muted">容器重新创建后会使用所选镜像中的版本。版本回退仅替换程序，不回退数据库。</p>
    </template>
    <p v-if="actionError" class="notice error" role="alert">{{ actionError }}</p>
    <div v-if="confirm" class="confirmation-panel" role="alert"><p>{{ confirmationText }}</p><div class="dialog-actions"><button :disabled="submitting" @click="confirm = null">取消</button><button class="primary" :disabled="busy" @click="execute">{{ submitting ? '提交中…' : '确认操作' }}</button></div></div>
  </Modal>
</template>
