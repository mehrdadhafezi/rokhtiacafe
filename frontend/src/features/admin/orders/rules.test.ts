import { describe, expect, it } from 'vitest'

import type { Order, OrderItem } from '../api'

import {
  canCancel,
  canRecordPayment,
  canRefund,
  canVoidPayment,
  nextTransition,
  remainingRial,
  secondsToExpiry,
  sortForBoard,
  wasteableItems,
} from './rules'

const item = (id: number, mode: OrderItem['inventory_mode']): OrderItem => ({
  id,
  product_id: id,
  name: 'x',
  inventory_mode: mode,
  quantity: 2,
  unit_price_rial: 10,
  line_total_rial: 20,
  wasted_quantity: 0,
})

const order = (o: Partial<Order>): Order =>
  ({
    id: 1,
    status: 'PENDING',
    payment_status: 'UNPAID',
    total_rial: 1_000_000,
    paid_rial: 0,
    refunded_rial: 0,
    created_at: '2026-10-19T10:00:00Z',
    items: [item(1, 'TRACKED'), item(2, 'UNTRACKED')],
    payments: [],
    ...o,
  }) as Order

describe('order rules', () => {
  it('walks the status flow', () => {
    expect(nextTransition.PENDING?.op).toBe('accept')
    expect(nextTransition.PREPARING?.op).toBe('ready')
    expect(nextTransition.READY?.op).toBe('complete')
    expect(nextTransition.COMPLETED).toBeUndefined()
  })

  it('takes payments only after acceptance and up to the total (no money on PENDING)', () => {
    expect(canRecordPayment(order({ status: 'PENDING' }))).toBe(false)
    expect(canRecordPayment(order({ status: 'PREPARING' }))).toBe(true)
    expect(canRecordPayment(order({ status: 'COMPLETED', paid_rial: 400_000 }))).toBe(true) // receivable
    expect(canRecordPayment(order({ status: 'READY', paid_rial: 1_000_000 }))).toBe(false)
    expect(canRecordPayment(order({ status: 'CANCELLED' }))).toBe(false)
    expect(canRecordPayment(order({ status: 'COMPLETED', refunded_rial: 5 }))).toBe(false)
    expect(remainingRial(order({ paid_rial: 300_000 }))).toBe(700_000)
  })

  it('cancels open orders only and records waste for tracked items once preparation started (F7)', () => {
    expect(canCancel(order({ status: 'READY' }))).toBe(true)
    expect(canCancel(order({ status: 'COMPLETED' }))).toBe(false)
    expect(wasteableItems(order({ status: 'PENDING' }))).toEqual([])
    expect(wasteableItems(order({ status: 'PREPARING' })).map((i) => i.id)).toEqual([1])
  })

  it('refunds only completed, fully paid orders and voids only live payment rows', () => {
    expect(canRefund(order({ status: 'COMPLETED', payment_status: 'PAID' }))).toBe(true)
    expect(canRefund(order({ status: 'READY', payment_status: 'PAID' }))).toBe(false)
    expect(canRefund(order({ status: 'COMPLETED', payment_status: 'UNPAID' }))).toBe(false)
    const live = { kind: 'PAYMENT', voided_at: null }
    expect(canVoidPayment(order({ status: 'READY' }), live)).toBe(true)
    expect(canVoidPayment(order({ status: 'CANCELLED' }), live)).toBe(false)
    expect(canVoidPayment(order({ status: 'COMPLETED', refunded_rial: 1 }), live)).toBe(false)
    expect(canVoidPayment(order({ status: 'READY' }), { kind: 'REFUND', voided_at: null })).toBe(false)
  })

  it('counts down to expiry and sorts the board oldest first', () => {
    expect(secondsToExpiry('2026-10-19T10:20:00Z', Date.parse('2026-10-19T10:19:30Z'))).toBe(30)
    expect(secondsToExpiry('2026-10-19T10:20:00Z', Date.parse('2026-10-19T10:21:00Z'))).toBe(-60)
    const a = order({ id: 2, created_at: '2026-10-19T10:05:00Z' })
    const b = order({ id: 1, created_at: '2026-10-19T10:00:00Z' })
    expect(sortForBoard([a, b]).map((o) => o.id)).toEqual([1, 2])
  })
})
