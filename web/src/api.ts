import { locale, t } from './i18n'

let csrfToken = ''
let onUnauthorized: (() => void) | null = null

export function setCSRF(value: string) { csrfToken = value }

// Registered by App so an expired session drops back to the login screen
// instead of leaving a polling view frozen on stale data.
export function setUnauthorizedHandler(handler: () => void) { onUnauthorized = handler }

export function errorMessage(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error)
  return message || t('api.unreachable')
}

// Wraps an action so a rejected request always reaches the user. Without this
// every write silently swallows the server's explanation (port conflict, quota
// exhausted, wrong password) and the UI looks like nothing happened.
export function guard(notify: (message: string) => void) {
  return <A extends unknown[]>(action: (...args: A) => Promise<unknown>) => async (...args: A) => {
    try { await action(...args) } catch (error) { notify(errorMessage(error)) }
  }
}

export async function api<T>(url: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body) headers.set('Content-Type', 'application/json')
  if (options.method && !['GET', 'HEAD'].includes(options.method)) headers.set('X-CSRF-Token', csrfToken)
  let response: Response
  try {
    response = await fetch(url, { ...options, headers, credentials: 'same-origin' })
  } catch {
    throw new Error(t('api.unreachable'))
  }
  const data = await response.json().catch(() => ({}))
  if (!response.ok) {
    if (response.status === 401 && url !== '/api/login') onUnauthorized?.()
    const message = locale.value === 'en' ? (data.errorEn || data.error) : data.error
    const error = new Error(message || t('api.failed', { status: response.status })) as Error & { status?: number }
    error.status = response.status
    throw error
  }
  return data as T
}

export const post = <T>(url: string, body: unknown = {}) => api<T>(url, { method: 'POST', body: JSON.stringify(body) })
export const put = <T>(url: string, body: unknown) => api<T>(url, { method: 'PUT', body: JSON.stringify(body) })
export const del = <T>(url: string) => api<T>(url, { method: 'DELETE', body: JSON.stringify({}) })
