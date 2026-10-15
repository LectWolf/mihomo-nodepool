import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { connect } from 'node:net'
import { spawn } from 'node:child_process'
import { mkdirSync, mkdtempSync, openSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { chromium } from 'playwright'

if (!process.env.MYSQL_DATABASE?.startsWith('nodepool_browser_test')) throw new Error('Browser tests require an isolated nodepool_browser_test database')
const artifacts = process.env.NODEPOOL_ARTIFACT_DIR || mkdtempSync(join(tmpdir(), 'nodepool-browser-'))
mkdirSync(artifacts, { recursive: true })
const listen = server => new Promise(resolve => server.listen(0, '127.0.0.1', () => resolve(server.address().port)))
const proxy = createServer((req, res) => { res.writeHead(405).end() })
let targetPort = 0
let tunnelCount = 0
proxy.on('connect', (req, client, head) => {
  if (req.url !== `127.0.0.1:${targetPort}`) { client.end('HTTP/1.1 502 Bad Gateway\r\n\r\n'); return }
  const upstream = connect(targetPort, '127.0.0.1', () => { tunnelCount++; client.write('HTTP/1.1 200 Connection Established\r\n\r\n'); if (head.length) upstream.write(head); upstream.pipe(client); client.pipe(upstream) })
  upstream.on('error', () => client.destroy())
  client.on('error', () => upstream.destroy())
  client.on('close', () => upstream.destroy())
})
const proxyPort = await listen(proxy)
const yaml = `proxies:\n - {name: Browser Japan, type: http, server: 127.0.0.1, port: ${proxyPort}}\n - {name: 剩余流量, type: http, server: 127.0.0.1, port: ${proxyPort}, username: info}\n`
const target = createServer((req, res) => {
  if (req.url === '/sub') { res.writeHead(200, { 'Content-Type': 'text/yaml' }).end(yaml); return }
  res.writeHead(204).end()
})
targetPort = await listen(target)
const portProbe = createServer(); const appPort = await listen(portProbe); await new Promise(resolve => portProbe.close(resolve))
const base = `http://127.0.0.1:${appPort}`
const app = spawn(resolve(process.env.NODEPOOL_BINARY || `build/nodepool${process.platform === 'win32' ? '.exe' : ''}`), [], {
  env: { ...process.env, HTTP_ADDR: `127.0.0.1:${appPort}`, DATA_DIR: join(artifacts, 'data'), NODEPOOL_ENV_FILE: join(artifacts, 'missing.env'), ADMIN_PASSWORD: 'browser-test-password' },
  stdio: ['ignore', openSync(join(artifacts, 'server.log'), 'w'), openSync(join(artifacts, 'server-error.log'), 'w')],
})
let browser
const until = async (fn, description, timeout = 16000) => {
  const end = Date.now() + timeout
  while (Date.now() < end) { if (await fn()) return; await delay(250) }
  throw new Error(`Timed out: ${description}`)
}
try {
  await until(async () => { try { return (await fetch(base + '/healthz')).ok } catch { return false } }, 'application startup')
  browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}) })
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, colorScheme: 'dark' })
  const pageErrors = []; page.on('pageerror', error => pageErrors.push(error.message))
  await page.goto(base)
  await page.getByPlaceholder('请输入密码').waitFor()
  assert.equal(await page.locator('html').getAttribute('data-theme'), 'dark', 'first visit must follow system dark mode')
  await page.getByPlaceholder('请输入密码').fill('browser-test-password')
  await page.getByRole('button', { name: /登录控制台/ }).click()
  await page.getByRole('heading', { name: '节点总览', exact: true }).waitFor()
  const api = async (path, options = {}) => {
    const result = await page.request.fetch(base + '/api/v1' + path, options)
    assert.ok(result.ok(), `${path} returned ${result.status()}: ${await result.text()}`)
    return result.json()
  }
  // This database is exclusively owned by the test. Keep repeated runs isolated.
  for (const source of (await api('/sources')).items) await api(`/sources/${source.id}`, { method: 'DELETE' })
  await page.getByRole('link', { name: /运行设置/ }).click()
  await page.getByLabel('测试 URL', { exact: true }).fill(`http://127.0.0.1:${targetPort}/delay`)
  assert.equal(new URL(page.url()).pathname, '/settings')
  await page.getByRole('button', { name: '保存设置', exact: true }).click()
  await until(async () => (await api('/settings')).test_url.endsWith('/delay'), 'save settings')
  await page.getByRole('link', { name: /来源管理/ }).click()
  assert.equal(new URL(page.url()).pathname, '/sources')
  const add = async (name, kind, content) => {
    await page.getByRole('button', { name: /添加来源/ }).first().click()
    const dialog = page.getByRole('dialog')
    await dialog.getByPlaceholder('留空使用地址或文件名称').fill(name)
    if (kind === 'file') {
      await dialog.getByRole('tab', { name: /本地文件/ }).click()
      await dialog.locator('input[type=file]').setInputFiles({ name: 'browser.yaml', mimeType: 'text/yaml', buffer: Buffer.from(content) })
    } else if (kind === 'links') {
      await dialog.getByRole('tab', { name: /节点链接/ }).click()
      await dialog.locator('textarea').fill(content)
    } else await dialog.locator('textarea').fill(content)
    await dialog.getByRole('button', { name: '开始导入', exact: true }).click()
    await dialog.getByRole('button', { name: '返回来源列表', exact: true }).click()
    await until(async () => (await api('/sources')).items.some(s => s.name.startsWith(name) && s.status === 'ready'), `import ${name}`)
  }
  await add('浏览器订阅', 'url', `http://127.0.0.1:${targetPort}/sub`)
  await add('浏览器文件', 'file', yaml.replace('Browser Japan', 'Browser Alias'))
  await add('浏览器链接', 'links', `socks5://127.0.0.1:${proxyPort}#Browser-Link`)
  let data = await api('/nodes'); assert.equal(data.total, 2)
  const httpNode = data.items.find(n => n.type === 'http'); assert.equal(httpNode.sources.length, 2)
  assert.ok(!data.items.some(n => n.name.includes('剩余')))
  await page.getByRole('link', { name: /节点总览/ }).click()
  await page.getByRole('button', { name: /更新列表/ }).click()
  assert.equal(new URL(page.url()).pathname, '/nodes')
  await page.getByPlaceholder('搜索节点名称或地址').fill('Browser Alias')
  await until(async () => (await page.locator('tbody tr').count()) === 1, 'node search')
  await until(async () => new URL(page.url()).searchParams.get('q') === 'Browser Alias', 'search route')
  await page.getByPlaceholder('搜索节点名称或地址').fill('no-such-node')
  await page.getByRole('heading', { name: '没有符合条件的节点' }).waitFor()
  await page.getByPlaceholder('搜索节点名称或地址').fill('')
  await page.getByRole('button', { name: /更新列表/ }).click()
  await api(`/nodes/${httpNode.id}/test`, { method: 'POST', data: {} })
  await until(async () => typeof (await api('/nodes')).items.find(n => n.id === httpNode.id)?.latency_ms === 'number', 'real Mihomo delay test')
  assert.ok(tunnelCount > 0, 'delay test did not traverse the local proxy')
  await page.getByRole('button', { name: /更新列表/ }).click()
  await page.screenshot({ path: join(artifacts, 'nodes-desktop.png'), fullPage: true })
  await page.getByRole('button', { name: '卡片视图', exact: true }).click()
  await page.locator('.node-card').first().waitFor()
  assert.equal(new URL(page.url()).searchParams.get('view'), 'cards')
  await page.screenshot({ path: join(artifacts, 'nodes-dark-cards.png'), fullPage: true })
  await page.reload()
  await page.locator('.node-card').first().waitFor()
  assert.equal(await page.locator('html').getAttribute('data-theme'), 'dark')
  await page.getByRole('combobox', { name: '外观模式', exact: true }).click()
  await page.getByRole('option', { name: '浅色模式', exact: true }).click()
  assert.equal(await page.locator('html').getAttribute('data-theme'), 'light')
  await page.getByRole('button', { name: '列表视图', exact: true }).click()
  await page.locator('tbody tr').first().waitFor()
  await page.screenshot({ path: join(artifacts, 'nodes-light-list.png'), fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await delay(200)
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'mobile page overflows horizontally')
  await page.screenshot({ path: join(artifacts, 'nodes-mobile.png'), fullPage: true })
  await page.getByRole('button', { name: '卡片视图', exact: true }).click()
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'mobile cards overflow horizontally')
  await page.screenshot({ path: join(artifacts, 'nodes-mobile-cards.png'), fullPage: true })
  await page.getByRole('button', { name: '列表视图', exact: true }).click()
  await page.setViewportSize({ width: 1440, height: 1000 })
  const sources = (await api('/sources')).items
  const duplicate = sources.find(s => s.name.startsWith('浏览器文件'))
  await api(`/sources/${duplicate.id}`, { method: 'DELETE' })
  data = await api('/nodes'); assert.equal(data.total, 2); assert.equal(data.items.find(n => n.id === httpNode.id).sources.length, 1)
  assert.deepEqual(pageErrors, [], 'browser runtime errors')
  await page.getByRole('button', { name: /退出登录/ }).click()
  await page.getByPlaceholder('请输入密码').waitFor()
  console.log(JSON.stringify({ result: 'passed', flows: ['login', 'settings', 'subscription import', 'file import', 'share-link import', 'cross-source dedup', 'exclusion', 'search', 'Mihomo delay', 'system dark theme', 'custom theme selector', 'list/cards and route reload', 'delete source preserves shared node', 'mobile layouts', 'logout'], tunnelCount, artifacts }))
} finally {
  if (browser) await browser.close()
  app.kill()
  proxy.closeAllConnections(); target.closeAllConnections(); proxy.close(); target.close()
}
