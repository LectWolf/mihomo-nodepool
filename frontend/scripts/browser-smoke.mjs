// Contract-based browser integration check. Build first; uses Chromium/Edge via CDP, no extra packages.
// Override BROWSER_PATH with an installed Chromium executable on other platforms.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile, mkdtemp, rm } from 'node:fs/promises'
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
const nodes = Array.from({ length: 60 }, (_, index) => ({ id: index + 1, name: `节点-${index + 1}`, type: 'ss', server: 'example.test', port: 443, latency_ms: 40 + index, status: 'healthy', country: 'United States', country_code: 'US', exit_ip: '192.0.2.1', ...(index === 0 ? { region: ' 加州 ', city: '洛杉矶' } : index === 1 ? { region: '美国', city: ' 美国 ' } : {}), last_checked_at: date, next_check_at: date, last_error: '', sources: [{ id: 1, name: '主要来源', node_name: '原名 A' }, { id: 2, name: '备用来源', node_name: '原名 B' }] }))
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  const send = (status, data) => { if (!res.destroyed) { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)) } }
  try {
    if (!url.pathname.startsWith('/api/v1')) {
      const relative = path.extname(url.pathname) ? url.pathname.slice(1) : 'index.html'
      const filename = path.resolve(root, 'dist', relative)
      if (!filename.startsWith(path.resolve(root, 'dist') + path.sep) && relative !== 'index.html') { res.writeHead(403); res.end(); return }
      const body = await readFile(filename)
      res.writeHead(200, { 'Content-Type': filename.endsWith('.js') ? 'text/javascript' : filename.endsWith('.css') ? 'text/css' : filename.endsWith('.svg') ? 'image/svg+xml' : 'text/html' }); res.end(body); return
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
const scratchRoot = process.env.PI_SCRATCH_DIR || (await import('node:os')).tmpdir()
const profile = await mkdtemp(path.join(scratchRoot, 'browser-smoke-'))
const browser = spawn(browserPath, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check', ...(process.env.CI ? ['--no-sandbox', '--disable-dev-shm-usage'] : []), '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] })
let browserError = ''
browser.stderr.on('data', chunk => { browserError = (browserError + chunk.toString()).slice(-4000) })
browser.on('error', error => { browserError = error.message })
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
  const expression = `(() => { const container = ${scope}; if (!container) return false; const button = [...container.querySelectorAll('button,a')].filter(${visible}).find(element => (element.getAttribute('aria-label') || element.textContent.trim()) === ${JSON.stringify(label)} || element.querySelector('span')?.textContent.trim() === ${JSON.stringify(label)}); if (!button || button.disabled) return false; button.click(); return true; })()`
  await eventually(() => evaluate(expression), `enabled button: ${label}`)
}
async function input(selector, value) {
  await evaluate(`(() => { const element = document.querySelector(${JSON.stringify(selector)}); if (!element) throw Error('Missing input'); element.value = ${JSON.stringify(value)}; element.dispatchEvent(new Event(element.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true })); })()`)
}
async function select(label, value) {
  await evaluate(`document.querySelector('[role="combobox"][aria-label="' + ${JSON.stringify(label)} + '"]').scrollIntoView({ block: 'center', behavior: 'instant' })`)
  await sleep(80)
  await evaluate(`(() => { const trigger = document.querySelector('[role="combobox"][aria-label="' + ${JSON.stringify(label)} + '"]'); if (!trigger || trigger.disabled) throw Error('Missing/enabled select: ' + ${JSON.stringify(label)}); trigger.focus(); trigger.click(); })()`)
  await eventually(() => evaluate(`!!document.querySelector('[role="listbox"] [role="option"][data-value="' + ${JSON.stringify(String(value))} + '"]')`), `option ${value}`)
  await evaluate(`document.querySelector('[role="listbox"] [role="option"][data-value="' + ${JSON.stringify(String(value))} + '"]').click()`)
}
async function key(key) {
  const codes = { Enter: 13, Escape: 27, Tab: 9, ArrowDown: 40, ArrowUp: 38, Home: 36, End: 35, ' ': 32 }
  await cdp('Input.dispatchKeyEvent', { type: 'keyDown', key, windowsVirtualKeyCode: codes[key] || key.toUpperCase().charCodeAt(0) })
  await cdp('Input.dispatchKeyEvent', { type: 'keyUp', key, windowsVirtualKeyCode: codes[key] || key.toUpperCase().charCodeAt(0) })
}
async function submit() { await evaluate('document.querySelector("dialog[open] form, .login-card").requestSubmit()') }
const check = name => console.log(`PASS ${name}`)
try {
  let debugPort
  await eventually(async () => {
    if (browser.exitCode !== null || browser.signalCode !== null) throw new Error(`Browser exited before readiness (${browser.exitCode ?? browser.signalCode}): ${browserError}`)
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
  await cdp('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'dark' }] })
  await cdp('Page.navigate', { url: appUrl })
  await hasText('管理员密码')
  await input('input[type=password]', 'wrong'); await submit(); await hasText('管理员密码错误')
  await input('input[type=password]', 'correct'); await submit(); await hasText('节点-1')
  assert.equal(await evaluate('location.search'), '', 'default node parameters stay out of URL')
  assert.equal(await evaluate('document.querySelector(".view-switch button").getAttribute("aria-label")'), '卡片视图')
  assert.equal(await evaluate('[...document.querySelectorAll(".view-switch button")].every(button => button.textContent.trim() === "")'), true, 'view toggles are icons only')
  assert.equal(await evaluate('document.querySelector(".node-card").textContent.includes("下次检测")'), false)
  assert.equal(await evaluate('document.querySelector(".node-card").textContent.includes("最近检测")'), false)
  assert.equal(await evaluate('(() => { const center = selector => { const box = document.querySelector(selector).getBoundingClientRect(); return box.top + box.height / 2 }; return Math.abs(center(".filters .search-field svg") - center(".filters .search-field input")) < 1 && Math.abs(center(".latency-filter > span:last-child") - center(".latency-filter input")) < 1 })()'), true, 'search icon and latency unit are vertically centered')
  assert.equal(await evaluate('document.querySelector("[aria-label=刷新节点]").textContent.trim()'), '', 'refresh is an icon button')
  if (process.env.NODEPOOL_ARTIFACT_DIR) {
    const { mkdir, writeFile } = await import('node:fs/promises')
    await mkdir(process.env.NODEPOOL_ARTIFACT_DIR, { recursive: true })
    const shot = await cdp('Page.captureScreenshot', { format: 'png' })
    await writeFile(path.join(process.env.NODEPOOL_ARTIFACT_DIR, 'nodes-dark-desktop.png'), Buffer.from(shot.data, 'base64'))
  }
  check('password login and JSON login error')
  assert.equal(await evaluate('document.documentElement.dataset.theme'), 'dark')
  assert.equal(await evaluate('getComputedStyle(document.querySelector(".panel")).backgroundColor'), 'rgb(27, 34, 41)')
  await select('外观模式', 'light')
  assert.equal(await evaluate('localStorage.getItem("nodepool-theme")'), 'light')
  if (process.env.NODEPOOL_ARTIFACT_DIR) {
    const { writeFile } = await import('node:fs/promises')
    const shot = await cdp('Page.captureScreenshot', { format: 'png' })
    await writeFile(path.join(process.env.NODEPOOL_ARTIFACT_DIR, 'nodes-light-desktop.png'), Buffer.from(shot.data, 'base64'))
  }
  await cdp('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }] })
  await cdp('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'dark' }] })
  await sleep(100)
  assert.equal(await evaluate('document.documentElement.dataset.theme'), 'light')
  await select('外观模式', 'system')
  await eventually(() => evaluate('document.documentElement.dataset.theme === "dark"'), 'follow dark system')
  await cdp('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }] })
  await eventually(() => evaluate('document.documentElement.dataset.theme === "light"'), 'follow light system')
  await select('外观模式', 'dark')
  check('first theme follows system, explicit preference persists, and system mode responds to changes')
  assert.equal(await evaluate('document.querySelectorAll("select").length'), 0)
  assert.equal(await evaluate('document.querySelector(".node-location-region").textContent'), '美国 · 加州 · 洛杉矶')
  assert.equal(await evaluate('document.querySelectorAll(".node-location-region")[1].textContent'), '美国')
  assert.equal(await evaluate('document.querySelectorAll(".node-location-region")[2].textContent'), '美国')
  await eventually(() => evaluate('document.querySelector("img.country-flag").naturalWidth > 0'), 'local SVG flag loaded')
  await evaluate('document.querySelector("[role=combobox][aria-label=协议]").focus()')
  await key(' '); await key('End'); await key('Enter')
  assert.equal(await evaluate('document.querySelector("[role=combobox][aria-label=协议]").textContent.trim()'), 'VMESS')
  await key('Home'); await key('Enter')
  await key('ArrowDown'); await key('ArrowDown'); await key('ArrowUp'); await key('Escape')
  assert.equal(await evaluate('document.activeElement.getAttribute("aria-label")'), '协议')
  assert.equal(await evaluate('document.querySelectorAll("[role=listbox]").length'), 0)
  await key('v'); await key('Enter')
  assert.equal(await evaluate('document.querySelector("[role=combobox][aria-label=协议]").textContent.trim()'), 'VMESS')
  await key('ArrowDown'); await key('Tab')
  assert.equal(await evaluate('document.activeElement.getAttribute("aria-label")'), '来源')
  await click('重置')
  check('custom selects: no native select, keyboard navigation/typeahead, Escape focus and Tab; localized/deduplicated locations and local SVG')
  await click('下一页'); await hasText('节点-51')
  assert.equal(requests.filter(item => item.route === '/nodes').at(-1).query.page, '2')
  await input('.filters input[type=search]', '节点-2'); await hasText('节点-20')
  assert.equal(requests.filter(item => item.route === '/nodes').at(-1).query.page, '1')
  await select('协议', 'ss')
  await input('.latency-filter input', '100')
  await sleep(450)
  assert.equal(requests.filter(item => item.route === '/nodes').at(-1).query.type, 'ss')
  assert.equal(requests.filter(item => item.route === '/nodes').at(-1).query.max_latency, '100')
  await select('来源', '2')
  await select('国家或地区', 'US')
  await select('健康状态', 'healthy')
  await select('排序', 'name')
  await sleep(450)
  const allFilters = requests.filter(item => item.route === '/nodes').at(-1).query
  assert.deepEqual([allFilters.source_id, allFilters.country, allFilters.status, allFilters.sort], ['2', 'US', 'healthy', 'name'])
  await input('.latency-filter input', '')
  await sleep(450)
  assert.ok(!('max_latency' in requests.filter(item => item.route === '/nodes').at(-1).query))
  const filtersBeforeView = requests.filter(item => item.route === '/nodes').at(-1).query
  assert.equal(await evaluate('document.querySelector("[aria-label=卡片视图]").getAttribute("aria-pressed")'), 'true')
  assert.equal(await evaluate('document.querySelector(".filters input[type=search]").value'), '节点-2')
  assert.deepEqual(requests.filter(item => item.route === '/nodes').at(-1).query, filtersBeforeView)
  await click('检测', 'document.querySelector(".node-card")'); await hasText('已加入检测队列')
  await evaluate('document.querySelector(".node-card .node-card-actions button:first-child").click()'); await hasText('原始名称：原名 B'); await evaluate('document.querySelector("dialog[open] .icon-button").click()')
  await cdp('Emulation.setDeviceMetricsOverride', { width: 320, height: 740, deviceScaleFactor: 1, mobile: true })
  assert.equal(await evaluate('document.documentElement.scrollWidth <= window.innerWidth'), true)
  await cdp('Page.reload'); await hasText('节点-20')
  assert.equal(await evaluate('document.documentElement.dataset.theme'), 'dark')
  assert.equal(await evaluate('new URL(location.href).searchParams.get("q")'), '节点-2')
  assert.equal(await evaluate('document.querySelector("[aria-label=卡片视图]").getAttribute("aria-pressed")'), 'true')
  await click('重置'); await hasText('节点-1')
  await select('每页条数', 25); await sleep(450)
  assert.equal(requests.filter(item => item.route === '/nodes').at(-1).query.page_size, '25')
  await click('下一页'); await hasText('节点-26')
  await evaluate('document.querySelector("[aria-label=列表视图]").click()'); await hasText('节点-26')
  assert.equal(requests.filter(item => item.route === '/nodes').at(-1).query.page, '2')
  assert.equal(await evaluate('new URL(location.href).searchParams.get("page")'), '2')
  assert.equal(await evaluate('new URL(location.href).searchParams.get("view")'), 'list')
  await evaluate('history.back()')
  await eventually(() => evaluate('document.querySelector("[aria-label=卡片视图]").getAttribute("aria-pressed") === "true"'), 'back restores card view')
  await evaluate('history.forward()')
  await eventually(() => evaluate('document.querySelector("[aria-label=列表视图]").getAttribute("aria-pressed") === "true"'), 'forward restores list view')
  await cdp('Page.reload'); await hasText('节点-26')
  assert.equal(await evaluate('new URL(location.href).searchParams.get("page")'), '2')
  check('URL keeps page/filter/view across reload, back and forward')
  await select('每页条数', 50); await sleep(450)
  await cdp('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1050, deviceScaleFactor: 1, mobile: false })
  check('cards preserve filters/page, persist on reload, share detection/details, custom page size and 320px layout')
  await click('重置'); await hasText('节点-1')
  await click('主要来源+1')
  await hasText('原始名称：原名 B'); await evaluate('document.querySelector("dialog[open] .icon-button").click()')
  await click('检测', 'document.querySelector("tbody tr")'); await hasText('已加入检测队列')
  await click('检测全部节点'); await hasText('全部有效节点已加入检测队列')
  assert.deepEqual(requests.find(item => item.route === '/nodes/test').body, {})
  check('pagination, filter queries, source associations and single/all detection')
  await input('.filters input[type=search]', 'slow'); await sleep(400)
  await input('.filters input[type=search]', '节点-2'); await sleep(1300)
  assert.equal(await evaluate('document.querySelector(".node-name").textContent'), '节点-2')
  await click('重置'); await sleep(400)
  check('late search response cancellation and clearing optional latency filter')
  await click('来源管理')
  assert.equal(await evaluate('location.pathname'), '/sources')
  await click('添加来源')
  await input('dialog textarea', 'https://good.example/sub\nhttps://fail.example/sub\ninvalid')
  await submit(); await hasText('1 个失败')
  await click('返回来源列表'); await eventually(async () => (await text()).includes('2 个失败'), 'async source partial failures')
  check('batch URL imports, async source polling and partial failures')
  await click('添加来源'); await click('本地文件', 'document.querySelector("dialog[open]")')
  await evaluate(`(() => { const transfer = new DataTransfer(); transfer.items.add(new File(['ss://one'], 'one.txt')); transfer.items.add(new File(['ss://two'], 'two.yaml')); const element = document.querySelector('input[type=file]'); element.files = transfer.files; element.dispatchEvent(new Event('change', {bubbles:true})); })()`)
  await submit(); await hasText('本次任务：'); await sleep(350); await click('返回来源列表')
  assert.equal(requests.filter(item => item.route === '/sources' && item.body?.kind === 'file').length, 2)
  assert.equal(requests.find(item => item.body?.name === 'one.txt').body.content, 'ss://one')
  await click('添加来源'); await click('节点链接', 'document.querySelector("dialog[open]")'); await input('dialog textarea', 'ss://pasted'); await submit(); await sleep(350); await click('返回来源列表')
  assert.equal(requests.filter(item => item.body?.kind === 'links').at(-1).body.content, 'ss://pasted')
  check('multiple local files read as text and pasted link import')
  await click('编辑', 'document.querySelector(".source-card")')
  await input('dialog input', '重命名来源'); await submit(); await hasText('重命名来源')
  const patch = requests.filter(item => item.method === 'PATCH').at(-1)
  assert.equal(patch.body.name, '重命名来源'); assert.ok(!('url' in patch.body))
  await evaluate('document.querySelector(".source-card [role=switch]").click()'); await sleep(150)
  assert.equal(requests.filter(item => item.method === 'PATCH').at(-1).body.enabled, false)
  await evaluate('document.querySelector(".source-card [role=switch]").click()'); await sleep(150)
  await click('刷新', 'document.querySelector(".source-card")'); await hasText('等待后台任务处理')
  await click('删除', 'document.querySelector(".source-card")'); assert.equal(requests.filter(item => item.method === 'DELETE').length, 0)
  await click('确认删除'); await eventually(() => !sources.some(item => item.name === '重命名来源'), 'source deletion')
  assert.ok(!requests.some(item => item.route === '/sources/refresh'))
  check('source edit preserves URL, enable/disable, individual refresh and confirmed deletion')
  await click('运行设置'); await hasText('全局排除规则')
  assert.equal(await evaluate('location.pathname'), '/settings')
  await input('.settings-main textarea', '('); await click('保存设置'); await hasText('排除正则无效')
  await input('.settings-main textarea', 'draft-pattern')
  await sleep(4500)
  assert.equal(await evaluate('document.querySelector(".settings-main textarea").value'), 'draft-pattern')
  await click('节点总览'); await click('运行设置')
  assert.equal(await evaluate('document.querySelector(".settings-main textarea").value'), 'draft-pattern')
  await click('保存设置'); await hasText('设置已保存')
  assert.deepEqual(Object.keys(requests.filter(item => item.method === 'PUT').at(-1).body).sort(), ['exclude_pattern', 'probe_concurrency', 'source_concurrency', 'test_url', 'timeout_ms'])
  check('settings validation, exact payload, and drafts retained through polling/navigation')
  await evaluate('document.querySelector(".version-button").click()'); await hasText('Release v2.0.0')
  await evaluate('document.querySelector("[role=combobox][aria-label=选择发布版本]").focus()')
  await key(' ')
  assert.equal(await evaluate('document.querySelector("dialog[open]").contains(document.querySelector("[role=listbox]"))'), true)
  assert.equal(await evaluate('document.querySelector("[role=listbox]").matches(":popover-open")'), true)
  assert.equal(await evaluate('(() => { const box = document.querySelector("[role=listbox]").getBoundingClientRect(); return box.left >= 0 && box.top >= 0 && box.right <= innerWidth && box.bottom <= innerHeight })()'), true)
  await key('Escape')
  assert.equal(await evaluate('!!document.querySelector("dialog[open]")'), true)
  await select('选择发布版本', 'v0.9.0'); await hasText('真实契约测试发布说明 v0.9.0')
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
  await evaluate('document.querySelector("dialog[open] .icon-button").click()')
  await sleep(150)
  const releasesAfterClose = requests.filter(item => item.route === '/releases').length
  check('real release notes, selected history rollback, local backup, update, non-Linux restart error and reconnect')
  await cdp('Page.navigate', { url: appUrl + '/nodes?page=2&page_size=25&view=cards&type=ss&country=US' })
  await hasText('节点-26')
  assert.equal(await evaluate('document.querySelector("[aria-label=卡片视图]").getAttribute("aria-pressed")'), 'true')
  assert.equal(await evaluate('document.querySelector("[aria-label=每页条数]").textContent.trim()'), '25')
  assert.equal(requests.filter(item => item.route === '/nodes').at(-1).query.country, 'US')
  await evaluate(`document.querySelector('nav a[href^="/sources"]').click()`)
  await input('.panel-heading .search-field input', '备用')
  await eventually(() => evaluate('new URL(location.href).searchParams.get("q") === "备用"'), 'source search URL')
  await evaluate('history.back()'); await hasText('节点-26')
  assert.equal(await evaluate('new URL(location.href).searchParams.get("page")'), '2')
  await evaluate('history.forward()'); await hasText('备用来源')
  assert.equal(await evaluate('document.querySelector(".panel-heading .search-field input").value'), '备用')
  await evaluate(`document.querySelector('nav a[href^="/nodes"]').click()`)
  await hasText('节点-26')
  await cdp('Page.navigate', { url: appUrl + '/nodes?page=999&page_size=25&view=list' })
  await hasText('节点-51')
  assert.equal(await evaluate('new URL(location.href).searchParams.get("page")'), '3')
  await select('每页条数', 50); await click('重置'); await hasText('节点-1')
  await select('外观模式', 'light')
  assert.equal(await evaluate('getComputedStyle(document.querySelector(".panel")).backgroundColor'), 'rgb(255, 255, 255)')
  check('direct page/filter/card URL, source/settings routes, source search history and out-of-range page normalization')
  await click('节点总览')
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
  await input('.filters input[type=search]', 'database-down'); await hasText('数据库暂不可用')
  databaseDown = false
  await click('重试连接'); await sleep(250)
  force401 = true
  await input('.filters input[type=search]', 'auth-expired'); await hasText('登录已过期')
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
