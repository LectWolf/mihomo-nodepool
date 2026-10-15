export const SYSTEM_ENDPOINT = '/api/v1/system'

export const CORE_NOT_INTEGRATED = 'not_integrated' as const

export interface DatabaseStatus {
  configured: boolean
  connected: boolean
}

export interface CoreStatus {
  state: typeof CORE_NOT_INTEGRATED
}

export interface SystemInfo {
  name: string
  version: string
  commit: string
  build_time: string
  database: DatabaseStatus
  core: CoreStatus
}

export class SystemResponseError extends Error {
  readonly status: number | undefined

  constructor(message: string, status?: number) {
    super(message)
    this.name = 'SystemResponseError'
    this.status = status
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function readText(record: Record<string, unknown>, key: string): string {
  const value = record[key]
  if (typeof value !== 'string' || value.length === 0) {
    throw new SystemResponseError(`系统接口缺少有效的 ${key}`)
  }
  return value
}

export function parseSystemInfo(value: unknown): SystemInfo {
  if (!isRecord(value)) {
    throw new SystemResponseError('系统接口返回的不是对象')
  }

  const database = value.database
  if (
    !isRecord(database) ||
    typeof database.configured !== 'boolean' ||
    typeof database.connected !== 'boolean'
  ) {
    throw new SystemResponseError('系统接口的 database 状态无效')
  }

  const core = value.core
  if (!isRecord(core) || core.state !== CORE_NOT_INTEGRATED) {
    throw new SystemResponseError('系统接口的 core.state 与约定的 not_integrated 不一致')
  }

  return {
    name: readText(value, 'name'),
    version: readText(value, 'version'),
    commit: readText(value, 'commit'),
    build_time: readText(value, 'build_time'),
    database: {
      configured: database.configured,
      connected: database.connected,
    },
    core: {
      state: CORE_NOT_INTEGRATED,
    },
  }
}

export function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError'
}

export async function fetchSystemInfo(signal: AbortSignal): Promise<SystemInfo> {
  let response: Response
  try {
    response = await fetch(SYSTEM_ENDPOINT, {
      method: 'GET',
      headers: { Accept: 'application/json' },
      cache: 'no-store',
      signal,
    })
  } catch (error) {
    if (isAbortError(error)) {
      throw error
    }
    throw new SystemResponseError(
      '无法连接后端。请确认 Go 服务已在 127.0.0.1:8080 监听，并且开发代理可用。',
    )
  }

  if (!response.ok) {
    throw new SystemResponseError(`系统接口返回 HTTP ${response.status}`, response.status)
  }

  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new SystemResponseError('系统接口返回的不是 JSON')
  }

  return parseSystemInfo(body)
}
