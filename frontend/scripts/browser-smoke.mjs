// Contract-based browser integration check. Build first; uses Chromium/Edge via CDP, no extra packages.
// Override BROWSER_PATH with an installed Chromium executable on other platforms.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile, mkdir, mkdtemp, rm } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const root = fileURLToPath(new URL('../', import.meta.url))
const browserPath = process.env.BROWSER_PATH || ['C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe', 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe', '/usr/bin/chromium', '/usr/bin/google-chrome'].find(existsSync)
if (!browserPath) throw new Error('Set BROWSER_PATH to an installed Chromium/Edge executable.')
assert.ok(existsSync(path.join(root, 'dist/index.html')), 'Run npm run build first')
const requests = []
const pageErrors = []
const externalRequests = []
let loggedIn = false
let databaseDown = false
let force401 = false
let restartAllowed = false
const date = '2026-01-01T00:00:00Z'
const makeSource = (id, name, kind = 'url') => ({ id, name, kind, enabled: true, exclude_pattern: '', status: 'ready', node_count: 1, excluded_count: 0, last_error: '', last_refresh_at: date, next_refresh_at: date, created_at: date })
let sources = [makeSource(1, '主要来源'), makeSource(2, '备用来源')]
let nextId = 3
let settings = { exclude_pattern: '流量|到期', test_url: 'https://www.gstatic.com/generate_204', timeout_ms: 5000, probe_concurrency: 16, source_concurrency: 3, refresh_hours: 6, check_hours: 6 }
const releases = { current_version: 'v1.0.0', latest_version: 'v2.0.0', update_available: true, can_update: true, can_rollback: true, need_restart: false, state: 'idle', message: '', releases: ['v2.0.0', 'v1.0.0', 'v0.9.0'].map(version => ({ version, name: `Release ${version}`, body: `真实契约测试发布说明 ${version}\n- 测试条目`, published_at: date, url: `https://github.com/example/test/releases/tag/${version}` })) }
let targetVersion = releases.current_version
const nodes = Array.from({ length: 60 }, (_, index) => ({ id: index + 1, name: `节点-${index + 1}`, type: 'ss', server: 'example.test', port: 443, latency_ms: 40 + index, status: 'healthy', country: '美国', country_code: 'US', exit_ip: '192.0.2.1', last_checked_at: date, next_check_at: date, last_error: '', sources: [{ id: 1, name: '主要来源', node_name: '原名 A' }, { id: 2, name: '备用来源', node_name: '原名 B' }] }))
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  const send = (status, data) => { if (!res.destroyed) { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)) } }
  try {
    if (!url.pathname.startsWith('/api/v1')) {
      const relative = url.pathname === '/' ? 'index.html' : url.pathname.slice(1)
      const filename = path.resolve(root, 'dist', relative)
      if (!filename.startsWith(path.resolve(root, 'dist') + path.sep) && relative !== 'index.html') { res.writeHead(403); res.end(); return }
      const body = await readFile(filename)
      res.writeHead(200, { 'Content-Type': filename.endsWith('.js') ? 'text/javascript' : filename.endsWith('.css') ? 'text/css' : 'text/html' }); res.end(body); return
    }
    const chunks = []
    for await (const chunk of req) chunks.push(chunk)
    const raw = Buffer.concat(chunks).toString()
    const body = raw ? JSON.parse(raw) : undefined
    const route = url.pathname.replace('/api/v1', '')
    requests.push({ route, method: req.method, body, query: Object.fromEntries(url.searchParams) })
    if (route === '/auth') { send(200, { authenticated: loggedIn, username: 'admin' }); return }
    if (route === '/auth/login') {
      if (body.password !== 'correct') { send(401, { error: '管理员密码错误' }); return }
      loggedIn = true; send(200, { authenticated: true }); return
    }
    if (route === '/auth/logout') { loggedIn = false; send(200, { authenticated: false }); return }
    if (!loggedIn || force401) { send(401, { error: '需要登录' }); return }
    if (route === '/system') { send(200, { name: 'Mihomo NodePool', version: releases.current_version, commit: 'browser-test', build_time: date, database: { configured: true, connected: !databaseDown }, core: { state: 'ready', version: 'test-core' }, tasks: { pending_sources: 0, pending_nodes: 0, active_sources: 0, active_nodes: 0 } }); return }
    if (databaseDown && !route.includes('release')) { send(503, { error: '测试数据库连接失败' }); return }
    if (route === '/sources' && req.method === 'GET') { send(200, { items: sources }); return }
    if (route === '/sources' && req.method === 'POST') {
      const source = { ...makeSource(nextId++, body.name, body.kind), exclude_pattern: body.exclude_pattern || '', status: 'queued' }
      sources.push(source)
      setTimeout(() => { source.status = body.url?.includes('fail') ? 'error' : 'ready'; source.last_error = source.status === 'error' ? '订阅内容无法解析' : '' }, 350)
      send(202, source); return
    }
    const sourceMatch = route.match(/^\/sources\/(\d+)(\/refresh)?$/)
    if (sourceMatch) {
      const source = sources.find(item => item.id === Number(sourceMatch[1]))
      if (!source) { send(404, { error: '来源不存在' }); return }
      if (req.method === 'DELETE') { sources = sources.filter(item => item !== source); send(200, { deleted: true }); return }
      if (sourceMatch[2]) { source.status = 'queued'; setTimeout(() => { source.status = 'ready'; source.last_error = '' }, 300); send(200, { queued: true }); return }
      Object.assign(source, body); send(200, source); return
    }
    if (route === '/nodes' && req.method === 'GET') {
      if (url.searchParams.get('q') === 'timeout') { setTimeout(() => send(200, {}), 30000).unref(); return }
      if (url.searchParams.get('q') === 'slow') await new Promise(resolve => setTimeout(resolve, 1000))
      const filtered = nodes.filter(node => !url.searchParams.get('q') || node.name.includes(url.searchParams.get('q')))
      const page = Number(url.searchParams.get('page')); const size = Number(url.searchParams.get('page_size'))
      send(200, { items: filtered.slice((page - 1) * size, page * size), total: filtered.length, page, page_size: size, stats: { total: 60, healthy: 60, unhealthy: 0, pending: 0 } }); return
    }
    if (/^\/nodes(?:\/\d+)?\/test$/.test(route)) { send(200, { queued: true }); return }
    if (route === '/options') { send(200, { types: ['ss', 'vmess'], countries: [{ code: 'US', name: '美国' }] }); return }
    if (route === '/settings') {
      if (req.method === 'PUT') {
        if (body.exclude_pattern === '(') { send(400, { error: '排除正则无效' }); return }
        settings = { ...settings, ...body }
      }
      send(200, settings); return
    }
    if (route === '/releases') { send(200, releases); return }
    if (route === '/system/update' || route === '/system/rollback') {
      targetVersion = body.version || (route.endsWith('rollback') ? 'backup-version' : releases.latest_version)
      releases.state = 'downloading'; releases.message = '正在下载程序'
      setTimeout(() => { releases.state = 'done'; releases.message = '程序文件已就绪'; releases.need_restart = true }, 500)
      send(202, { queued: true }); return
    }
    if (route === '/system/restart') {
      if (!restartAllowed) { send(400, { error: '仅 Linux 支持立即重启' }); return }
      releases.current_version = targetVersion; releases.need_restart = false; releases.state = 'idle'; releases.message = ''
      send(200, { restarting: true }); return
    }
    send(404, { error: `Unmatched test route: ${route}` })
  } catch (error) { send(500, { error: String(error) }) }
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const appUrl = `http://127.0.0.1:${server.address().port}`
await mkdir(path.join(root, 'node_modules/.cache'), { recursive: true })
const profile = await mkdtemp(path.join(root, 'node_modules/.cache/browser-smoke-'))
const browser = spawn(browserPath, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { stdio: 'ignore' })
let socket
let commandId = 0
const pending = new Map()
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))
async function eventually(fn, label, timeout = 10000) {
  const end = Date.now() + timeout
  while (Date.now() < end) { if (await fn()) return; await sleep(80) }
  throw new Error(`Timed out: ${label}`)
}
function cdp(method, params = {}) {
  return new Promise((resolve, reject) => {
    const id = ++commandId
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`CDP timeout: ${method}`)) }, 15000)
    pending.set(id, { resolve: value => { clearTimeout(timer); resolve(value) }, reject })
    socket.send(JSON.stringify({ id, method, params }))
  })
}
async function evaluate(expression) {
  const result = await cdp('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text + ': ' + result.result.description)
  return result.result.value
}
const text = () => evaluate('document.body.innerText')
const hasText = value => eventually(async () => (await text()).includes(value), value)
const visible = "element => !!element.getClientRects().length"
async function click(label, scope = 'document') {
  const expression = `(() => { const container = ${scope}; if (!container) return false; const button = [...container.querySelectorAll('button')].filter(${visible}).find(element => element.textContent.trim() === ${JSON.stringify(label)}); if (!button || button.disabled) return false; button.click(); return true; })()`
  await eventually(() => evaluate(expression), `enabled button: ${label}`)
}
async function input(selector, value) {
  await evaluate(`(() => { const element = document.querySelector(${JSON.stringify(selector)}); if (!element) throw Error('Missing input'); element.value = ${JSON.stringify(value)}; element.dispatchEvent(new Event(element.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true })); })()`)
}
async function submit() { await evaluate('document.querySelector("dialog[open] form, .login-card").requestSubmit()') }
const check = name => console.log(`PASS ${name}`)
try {
  let debugPort
  await eventually(async () => {
    try { debugPort = (await readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]; return !!debugPort } catch { return false }
  }, 'browser startup')
  const targets = await (await fetch(`http://127.0.0.1:${debugPort}/json/list`)).json()
  socket = new WebSocket(targets.find(target => target.type === 'page').webSocketDebuggerUrl)
  await new Promise(resolve => socket.addEventListener('open', resolve, { once: true }))
  socket.addEventListener('message', event => {
    const message = JSON.parse(event.data)
    if (message.id) { const item = pending.get(message.id); pending.delete(message.id); if (message.error) item?.reject(new Error(message.error.message)); else item?.resolve(message.result) }
    if (message.method === 'Runtime.exceptionThrown') pageErrors.push(message.params.exceptionDetails)
    if (message.method === 'Network.requestWillBeSent' && /^https?:/.test(message.params.request.url) && !message.params.request.url.startsWith(appUrl)) externalRequests.push(message.params.request.url)
  })
  await cdp('Runtime.enable'); await cdp('Network.enable'); await cdp('Page.enable')
  await cdp('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1050, deviceScaleFactor: 1, mobile: false })
  await cdp('Page.navigate', { url: appUrl })
  await hasText('管理员密码')
  await input('input[type=password]', 'wrong'); await submit(); await hasText('管理员密码错误')
  await input('input[type=password]', 'correct'); await submit(); await hasText('节点-1')
  check('password login and JSON login error')
  await click('→'); await hasText('节点-51')
  assert.equal(requests.filter(item => item.route === '/nodes').at(-1).query.page, '2')
  await input('.filters input[type=search]', '节点-2'); await hasText('节点-20')
  assert.equal(requests.filter(item => item.route === '/nodes').at(-1).query.page, '1')
  await input('.filters select', 'ss')
  await input('.latency-filter input', '100')
  await sleep(450)
  assert.equal(requests.filter(item => item.route === '/nodes').at(-1).query.type, 'ss')
  assert.equal(requests.filter(item => item.route === '/nodes').at(-1).query.max_latency, '100')
  await input('.filters label:nth-child(3) select', '2')
  await input('.filters label:nth-child(4) select', 'US')
  await input('.filters label:nth-child(5) select', 'healthy')
  await input('.filters label:nth-child(7) select', 'name')
  await sleep(450)
  const allFilters = requests.filter(item => item.route === '/nodes').at(-1).query
  assert.deepEqual([allFilters.source_id, allFilters.country, allFilters.status, allFilters.sort], ['2', 'US', 'healthy', 'name'])
  await input('.latency-filter input', '')
  await sleep(450)
  assert.ok(!('max_latency' in requests.filter(item => item.route === '/nodes').at(-1).query))
  await click('重置'); await hasText('节点-1')
  await click('主要来源+1')
  await hasText('原始名称：原名 B'); await click('×', 'document.querySelector("dialog[open]")')
  await click('检测', 'document.querySelector("tbody tr")'); await hasText('已加入检测队列')
  await click('◉ 检测全部节点'); await hasText('全部有效节点已加入检测队列')
  assert.deepEqual(requests.find(item => item.route === '/nodes/test').body, {})
  check('pagination, filter queries, source associations and single/all detection')
  await input('.filters input[type=search]', 'slow'); await sleep(400)
  await input('.filters input[type=search]', '节点-2'); await sleep(1300)
  assert.equal(await evaluate('document.querySelector(".node-name").textContent'), '节点-2')
  await click('重置'); await sleep(400)
  check('late search response cancellation and clearing optional latency filter')
  await click('▤来源管理2')
  await click('＋ 添加来源')
  await input('dialog textarea', 'https://good.example/sub\nhttps://fail.example/sub\ninvalid')
  await submit(); await hasText('1 个失败')
  await click('返回来源列表'); await eventually(async () => (await text()).includes('2 个失败'), 'async source partial failures')
  check('batch URL imports, async source polling and partial failures')
  await click('＋ 添加来源'); await click('本地文件', 'document.querySelector("dialog[open]")')
  await evaluate(`(() => { const transfer = new DataTransfer(); transfer.items.add(new File(['ss://one'], 'one.txt')); transfer.items.add(new File(['ss://two'], 'two.yaml')); const element = document.querySelector('input[type=file]'); element.files = transfer.files; element.dispatchEvent(new Event('change', {bubbles:true})); })()`)
  await submit(); await hasText('本次任务：'); await sleep(350); await click('返回来源列表')
  assert.equal(requests.filter(item => item.route === '/sources' && item.body?.kind === 'file').length, 2)
  assert.equal(requests.find(item => item.body?.name === 'one.txt').body.content, 'ss://one')
  await click('＋ 添加来源'); await click('节点链接', 'document.querySelector("dialog[open]")'); await input('dialog textarea', 'ss://pasted'); await submit(); await sleep(350); await click('返回来源列表')
  assert.equal(requests.filter(item => item.body?.kind === 'links').at(-1).body.content, 'ss://pasted')
  check('multiple local files read as text and pasted link import')
  await click('编辑', 'document.querySelector(".source-card")')
  await input('dialog input', '重命名来源'); await submit(); await hasText('重命名来源')
  const patch = requests.filter(item => item.method === 'PATCH').at(-1)
  assert.equal(patch.body.name, '重命名来源'); assert.ok(!('url' in patch.body))
  await evaluate('document.querySelector(".source-card [role=switch]").click()'); await sleep(150)
  assert.equal(requests.filter(item => item.method === 'PATCH').at(-1).body.enabled, false)
  await evaluate('document.querySelector(".source-card [role=switch]").click()'); await sleep(150)
  await click('↻ 刷新', 'document.querySelector(".source-card")'); await hasText('等待后台任务处理')
  await click('删除', 'document.querySelector(".source-card")'); assert.equal(requests.filter(item => item.method === 'DELETE').length, 0)
  await click('确认删除'); await eventually(() => !sources.some(item => item.name === '重命名来源'), 'source deletion')
  assert.ok(!requests.some(item => item.route === '/sources/refresh'))
  check('source edit preserves URL, enable/disable, individual refresh and confirmed deletion')
  await click('⚙运行设置'); await hasText('全局排除规则')
  await input('.settings-main textarea', '('); await click('保存设置'); await hasText('排除正则无效')
  await input('.settings-main textarea', 'draft-pattern')
  await sleep(4500)
  assert.equal(await evaluate('document.querySelector(".settings-main textarea").value'), 'draft-pattern')
  await click('◈节点总览'); await click('⚙运行设置')
  assert.equal(await evaluate('document.querySelector(".settings-main textarea").value'), 'draft-pattern')
  await click('保存设置'); await hasText('设置已保存')
  assert.deepEqual(Object.keys(requests.filter(item => item.method === 'PUT').at(-1).body).sort(), ['exclude_pattern', 'probe_concurrency', 'source_concurrency', 'test_url', 'timeout_ms'])
  check('settings validation, exact payload, and drafts retained through polling/navigation')
  await evaluate('document.querySelector(".version-button").click()'); await hasText('Release v2.0.0')
  await input('dialog select', 'v0.9.0'); await hasText('真实契约测试发布说明 v0.9.0')
  await click('回退到所选历史版'); await click('确认操作'); await hasText('程序文件已准备好')
  assert.equal(requests.filter(item => item.route === '/system/rollback').at(-1).body.version, 'v0.9.0')
  await click('立即重启'); await click('确认操作'); await hasText('仅 Linux 支持立即重启')
  restartAllowed = true
  await click('立即重启'); await click('确认操作'); await hasText('正在重新连接服务')
  await eventually(async () => !(await text()).includes('正在重新连接服务'), 'restart reconnect', 15000)
  await evaluate('document.querySelector(".version-button").click()'); await hasText('v0.9.0')
  await click('恢复本地 backup'); await click('确认操作'); await hasText('程序文件已准备好')
  assert.deepEqual(requests.filter(item => item.route === '/system/rollback').at(-1).body, {})
  await click('立即重启'); await click('确认操作'); await hasText('正在重新连接服务')
  await eventually(async () => !(await text()).includes('正在重新连接服务'), 'backup restart reconnect', 15000)
  await evaluate('document.querySelector(".version-button").click()'); await hasText('Release v2.0.0')
  await click('安装所选版本'); await click('确认操作'); await hasText('程序文件已准备好')
  assert.equal(requests.filter(item => item.route === '/system/update').at(-1).body.version, 'v2.0.0')
  await click('×', 'document.querySelector("dialog[open]")')
  await sleep(150)
  const releasesAfterClose = requests.filter(item => item.route === '/releases').length
  check('real release notes, selected history rollback, local backup, update, non-Linux restart error and reconnect')
  await click('◈节点总览')
  await input('.filters input[type=search]', 'timeout')
  await eventually(async () => (await text()).includes('请求超时'), 'fetch timeout guidance', 25000)
  await input('.filters input[type=search]', '')
  await eventually(async () => !(await text()).includes('请求超时'), 'recovery after request timeout')
  assert.equal(requests.filter(item => item.route === '/releases').length, releasesAfterClose)
  check('20-second fetch timeout, recovery and release polling cleanup after closing dialog')
  await cdp('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true })
  assert.equal(await evaluate('document.documentElement.scrollWidth <= window.innerWidth'), true)
  assert.equal(await evaluate('!!document.querySelector(".mobile-logout").getClientRects().length'), true)
  await cdp('Emulation.setDeviceMetricsOverride', { width: 320, height: 740, deviceScaleFactor: 1, mobile: true })
  assert.equal(await evaluate('document.documentElement.scrollWidth <= window.innerWidth'), true)
  databaseDown = true
  await click('↻ 更新列表'); await hasText('数据库暂不可用')
  databaseDown = false
  await click('重试连接'); await sleep(250)
  force401 = true
  await click('↻ 更新列表'); await hasText('登录已过期')
  assert.ok(!await evaluate('!!document.querySelector(".app-shell")'))
  check('responsive layout, mobile logout, 503 guidance and 401 returns to login')
  await sleep(150)
  const requestsAfterLogout = requests.length
  await sleep(5500)
  assert.equal(requests.length, requestsAfterLogout)
  check('authenticated polling timers cleaned after session expiration')
  assert.equal(pageErrors.length, 0, JSON.stringify(pageErrors))
  assert.equal(externalRequests.length, 0, JSON.stringify(externalRequests))
  check('no uncaught browser errors or external font/image/network requests')
  console.log(`Browser smoke passed (${requests.length} API requests). Fixtures are test-only; no live backend was tested.`)
} finally {
  if (socket?.readyState === WebSocket.OPEN) {
    socket.send(JSON.stringify({ id: ++commandId, method: 'Browser.close' }))
    socket.close()
    await sleep(150)
  }
  browser.kill()
  server.closeAllConnections(); await new Promise(resolve => server.close(resolve))
  await rm(profile, { recursive: true, force: true, maxRetries: 3, retryDelay: 200 }).catch(() => {})
}
