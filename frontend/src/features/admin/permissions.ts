/**
 * Client-side mirror of the backend policy table (internal/auth/policy.go). It only decides what
 * the UI shows; the server enforces every action again (AC-33).
 */

export type Role = 'ADMIN' | 'OPERATOR'

export type Action =
  | 'dashboard.view'
  | 'dashboard.revenue'
  | 'orders.view'
  | 'orders.transition'
  | 'payments.record'
  | 'orders.cancel_prepaid'
  | 'payments.refund'
  | 'payments.void'
  | 'ordering.toggle'
  | 'catalog.view'
  | 'catalog.availability'
  | 'catalog.manage'
  | 'inventory.view_qty'
  | 'inventory.view_cost'
  | 'inventory.manage'
  | 'expenses.manage'
  | 'reports.view'
  | 'users.manage'
  | 'audit.view'
  | 'self.password'

const operatorActions: ReadonlySet<Action> = new Set<Action>([
  'dashboard.view',
  'orders.view',
  'orders.transition',
  'payments.record',
  'orders.cancel_prepaid',
  'catalog.view',
  'catalog.availability',
  'inventory.view_qty',
  'self.password',
])

export function can(role: Role | undefined, action: Action): boolean {
  if (role === 'ADMIN') return true
  if (role === 'OPERATOR') return operatorActions.has(action)
  return false
}
