import { cpSync, mkdirSync, readdirSync, rmSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const frontend = join(root, 'frontend')
const backend = join(root, 'backend')
const generated = join(backend, 'internal', 'web', 'dist')

function run(command, args, cwd) {
  const result = spawnSync(command, args, { cwd, stdio: 'inherit' })
  if (result.error) throw result.error
  if (result.status !== 0) process.exit(result.status ?? 1)
}

if (process.platform === 'win32') {
  run(process.env.ComSpec || 'cmd.exe', ['/d', '/s', '/c', 'npm run build'], frontend)
} else {
  run('npm', ['run', 'build'], frontend)
}
mkdirSync(generated, { recursive: true })
for (const entry of readdirSync(generated)) {
  if (entry !== '.gitkeep') rmSync(join(generated, entry), { recursive: true, force: true })
}
cpSync(join(frontend, 'dist'), generated, { recursive: true })
mkdirSync(join(root, 'build'), { recursive: true })

const version = process.env.NODEPOOL_VERSION || 'dev'
const revision = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' })
const changes = spawnSync('git', ['status', '--porcelain', '--untracked-files=normal'], { cwd: root, encoding: 'utf8' })
const gitCommit = revision.status === 0 && /^[a-f0-9]{40,64}$/.test(revision.stdout.trim()) ? revision.stdout.trim() + (changes.status === 0 && changes.stdout.trim() ? '-dirty' : '') : 'unknown'
const commit = process.env.NODEPOOL_COMMIT || gitCommit
const builtAt = process.env.NODEPOOL_BUILD_TIME || new Date().toISOString()
for (const value of [version, commit, builtAt]) {
  if (!/^[a-zA-Z0-9._:+-]+$/.test(value)) throw new Error('Invalid build metadata')
}
const pkg = 'github.com/LectWolf/mihomo-nodepool/backend/internal/buildinfo'
const flags = `-s -w -X ${pkg}.Version=${version} -X ${pkg}.Commit=${commit} -X ${pkg}.BuildTime=${builtAt}`
const extension = (process.env.GOOS || process.platform) === 'windows' || (!process.env.GOOS && process.platform === 'win32') ? '.exe' : ''
run('go', ['build', '-trimpath', '-ldflags', flags, '-o', join(root, 'build', `nodepool${extension}`), './cmd/nodepool'], backend)
