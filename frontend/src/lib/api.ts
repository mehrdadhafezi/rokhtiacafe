/**
 * Fetch wrapper for the `/api/v1` REST API (docs/API-CONTRACT.md §2).
 *
 * - JSON in/out (FormData bodies are passed through for image uploads).
 * - Cookies are always sent; the staff session lives in an HttpOnly cookie.
 * - `X-CSRF-Token` is attached to every unsafe method once a token is known
 *   (it comes from `/auth/login` and `/auth/me`, see SECURITY §4).
 * - Non-2xx responses become an `ApiError` carrying the stable error code.
 */

export const API_BASE = '/api/v1'

/** Error codes defined by the API contract, plus two client-side ones. */
export type ApiErrorCode =
  | 'BAD_REQUEST'
  | 'UNAUTHENTICATED'
  | 'FORBIDDEN'
  | 'CSRF_FAILED'
  | 'NOT_FOUND'
  | 'ILLEGAL_TRANSITION'
  | 'INSUFFICIENT_STOCK'
  | 'IDEMPOTENCY_KEY_REUSED'
  | 'ORDER_EXPIRED'
  | 'PRICE_CHANGED'
  | 'PURCHASE_ALREADY_CONSUMED'
  | 'PAYMENT_STATE_CONFLICT'
  | 'CONFLICT'
  | 'PAYLOAD_TOO_LARGE'
  | 'UNSUPPORTED_MEDIA_TYPE'
  | 'VALIDATION_ERROR'
  | 'PRODUCT_UNAVAILABLE'
  | 'RATE_LIMITED'
  | 'TOO_MANY_OPEN_ORDERS'
  | 'ORDERING_CLOSED'
  | 'ORDERING_BUSY'
  | 'BUSY_RETRY'
  | 'INTERNAL'
  /** Client-side: the request never got a response (offline, DNS, proxy down). */
  | 'NETWORK_ERROR'
  /** Client-side: a response that is not a valid error envelope. */
  | 'UNKNOWN'

export type ErrorDetails = Record<string, unknown>

export class ApiError extends Error {
  readonly status: number
  readonly code: ApiErrorCode
  readonly details: ErrorDetails
  readonly requestId: string | null
  /** Seconds from the `Retry-After` header (429), if present. */
  readonly retryAfter: number | null

  constructor(init: {
    status: number
    code: ApiErrorCode
    message: string
    details?: ErrorDetails
    requestId?: string | null
    retryAfter?: number | null
  }) {
    super(init.message)
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.details = init.details ?? {}
    this.requestId = init.requestId ?? null
    this.retryAfter = init.retryAfter ?? null
  }
}

export function isApiError(err: unknown, code?: ApiErrorCode): err is ApiError {
  return err instanceof ApiError && (code === undefined || err.code === code)
}

let csrfToken: string | null = null

/** Remember the CSRF token returned by `/auth/login` or `/auth/me`; `null` on logout. */
export function setCsrfToken(token: string | null) {
  csrfToken = token
}

export type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export interface RequestOptions {
  method?: Method
  /** Serialized as JSON unless it is a FormData. */
  body?: unknown
  /** Extra headers, e.g. `Idempotency-Key` or `X-Tracking-Token`. */
  headers?: Record<string, string>
  signal?: AbortSignal
}

export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET'
  const headers = new Headers(options.headers)
  headers.set('Accept', 'application/json')

  let body: BodyInit | undefined
  if (options.body instanceof FormData) {
    body = options.body
  } else if (options.body !== undefined) {
    headers.set('Content-Type', 'application/json')
    body = JSON.stringify(options.body)
  }
  if (method !== 'GET' && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }

  let res: Response
  try {
    res = await fetch(API_BASE + path, { method, headers, body, credentials: 'include', signal: options.signal })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    throw new ApiError({ status: 0, code: 'NETWORK_ERROR', message: 'network error' })
  }

  if (!res.ok) throw await toApiError(res)
  if (res.status === 204) return undefined as T
  const text = await res.text()
  return (text ? JSON.parse(text) : undefined) as T
}

async function toApiError(res: Response): Promise<ApiError> {
  const requestId = res.headers.get('X-Request-Id')
  const retryAfterHeader = res.headers.get('Retry-After')
  const retryAfter = retryAfterHeader && /^\d+$/.test(retryAfterHeader) ? Number(retryAfterHeader) : null
  const unknown = new ApiError({ status: res.status, code: 'UNKNOWN', message: `HTTP ${res.status}`, requestId, retryAfter })

  let payload: unknown
  try {
    payload = await res.json()
  } catch {
    return unknown
  }
  const error = isRecord(payload) ? payload.error : undefined
  if (!isRecord(error) || typeof error.code !== 'string') return unknown
  return new ApiError({
    status: res.status,
    code: error.code as ApiErrorCode,
    message: typeof error.message === 'string' ? error.message : error.code,
    details: isRecord(error.details) ? error.details : {},
    requestId,
    retryAfter,
  })
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

type Opts = Omit<RequestOptions, 'method' | 'body'>

export const api = {
  get: <T>(path: string, opts?: Opts) => apiRequest<T>(path, { ...opts, method: 'GET' }),
  post: <T>(path: string, body?: unknown, opts?: Opts) => apiRequest<T>(path, { ...opts, method: 'POST', body }),
  put: <T>(path: string, body?: unknown, opts?: Opts) => apiRequest<T>(path, { ...opts, method: 'PUT', body }),
  patch: <T>(path: string, body?: unknown, opts?: Opts) => apiRequest<T>(path, { ...opts, method: 'PATCH', body }),
  delete: <T>(path: string, opts?: Opts) => apiRequest<T>(path, { ...opts, method: 'DELETE' }),
}
