let csrfToken = ''

export function setCSRF(value: string) { csrfToken = value }

export async function api<T>(url: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body) headers.set('Content-Type', 'application/json')
  if (options.method && !['GET', 'HEAD'].includes(options.method)) headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(url, { ...options, headers, credentials: 'same-origin' })
  const data = await response.json().catch(() => ({}))
  if (!response.ok) {
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
import { locale, t } from './i18n'
