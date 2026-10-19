import { describe, expect, it } from 'vitest'

import { canCancel, cancelReasonText, isFinal, paymentText, pollInterval, stepIndex } from './status'

describe('order status helpers', () => {
  it('polls every 5 s until the order is final', () => {
    expect(pollInterval(undefined)).toBe(5000)
    expect(pollInterval({ status: 'PREPARING', expired: false })).toBe(5000)
    expect(pollInterval({ status: 'COMPLETED', expired: false })).toBe(false)
    expect(pollInterval({ status: 'CANCELLED', expired: false })).toBe(false)
    expect(pollInterval({ status: 'PENDING', expired: true })).toBe(false)
  })

  it('allows customer cancellation only while pending and not expired', () => {
    expect(canCancel({ status: 'PENDING', expired: false })).toBe(true)
    expect(canCancel({ status: 'PENDING', expired: true })).toBe(false)
    expect(canCancel({ status: 'PREPARING', expired: false })).toBe(false)
  })

  it('maps progress steps and cancellation reasons', () => {
    expect(stepIndex('PENDING')).toBe(0)
    expect(stepIndex('COMPLETED')).toBe(3)
    expect(stepIndex('CANCELLED')).toBe(-1)
    expect(isFinal({ status: 'READY', expired: false })).toBe(false)
    expect(cancelReasonText({ status: 'CANCELLED', cancel_reason: 'EXPIRED', expired: false })).toContain('خودکار')
    expect(cancelReasonText({ status: 'CANCELLED', cancel_reason: 'CUSTOMER', expired: false })).toContain('خودتان')
    expect(cancelReasonText({ status: 'CANCELLED', cancel_reason: 'STAFF', expired: false })).toContain('کافه')
    expect(cancelReasonText({ status: 'PENDING', cancel_reason: null, expired: true })).toContain('منقضی')
    expect(cancelReasonText({ status: 'READY', cancel_reason: null, expired: false })).toBeNull()
  })

  it('hides the pay-at-counter line for orders that will not be fulfilled', () => {
    expect(paymentText({ payment_status: 'UNPAID', status: 'PENDING', expired: false })).toContain('صندوق')
    expect(paymentText({ payment_status: 'UNPAID', status: 'CANCELLED', expired: false })).toBeNull()
    expect(paymentText({ payment_status: 'UNPAID', status: 'PENDING', expired: true })).toBeNull()
    expect(paymentText({ payment_status: 'PAID', status: 'COMPLETED', expired: false })).toBe('پرداخت شده')
    // A recorded refund is not money handed back yet: the customer collects it at the counter.
    expect(paymentText({ payment_status: 'REFUNDED', status: 'COMPLETED', expired: false })).toContain('دریافت کنید')
  })
})
