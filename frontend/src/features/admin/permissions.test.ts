import { describe, expect, it } from 'vitest'

import { can } from './permissions'

describe('role permissions (mirror of internal/auth/policy.go)', () => {
  it('lets OPERATOR run the floor but not manage money, catalog or users (AC-33)', () => {
    for (const a of ['orders.view', 'orders.transition', 'payments.record', 'catalog.availability', 'inventory.view_qty'] as const) {
      expect(can('OPERATOR', a)).toBe(true)
    }
    for (const a of [
      'catalog.manage',
      'inventory.manage',
      'inventory.view_cost',
      'expenses.manage',
      'reports.view',
      'users.manage',
      'payments.refund',
      'payments.void',
      'ordering.toggle',
      'dashboard.revenue',
      'push.manage',
    ] as const) {
      expect(can('OPERATOR', a)).toBe(false)
    }
  })

  it('lets ADMIN do everything and nobody else anything', () => {
    expect(can('ADMIN', 'users.manage')).toBe(true)
    expect(can(undefined, 'orders.view')).toBe(false)
  })
})
