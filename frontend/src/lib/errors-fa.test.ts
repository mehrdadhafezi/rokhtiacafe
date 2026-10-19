import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

import { ApiError } from './api'
import { errorMessageFa, errorMessagesFa } from './errors-fa'

// Error codes from the table in docs/API-CONTRACT.md §2 (rows like "| 409 | `PRICE_CHANGED` | ...").
function contractCodes(): string[] {
  const doc = readFileSync(resolve(import.meta.dirname, '../../../docs/API-CONTRACT.md'), 'utf8')
  return [...doc.matchAll(/^\|\s*\d{3}\s*\|\s*`([A-Z_]+)`/gm)].map((m) => m[1]!)
}

describe('errors-fa', () => {
  it('has a Persian message for every error code in the API contract', () => {
    const codes = contractCodes()
    expect(codes.length).toBeGreaterThan(20)
    const missing = codes.filter((c) => !(c in errorMessagesFa))
    expect(missing).toEqual([])
  })

  it('maps ApiError codes to Persian text', () => {
    const err = new ApiError({ status: 503, code: 'ORDERING_CLOSED', message: 'ordering closed' })
    expect(errorMessageFa(err)).toBe(errorMessagesFa.ORDERING_CLOSED)
  })

  it('falls back for unknown codes and non-API errors', () => {
    const odd = new ApiError({ status: 418, code: 'SOMETHING_NEW' as never, message: 'x' })
    expect(errorMessageFa(odd)).toBe(errorMessagesFa.UNKNOWN)
    expect(errorMessageFa(new Error('boom'))).toBe(errorMessagesFa.UNKNOWN)
    expect(errorMessageFa(undefined)).toBe(errorMessagesFa.UNKNOWN)
  })
})
