/** TanStack Query keys for the staff app. */
export const dashboardKey = ['admin', 'dashboard'] as const
export const ordersKey = ['admin', 'orders'] as const
export const orderKey = (id: number) => ['admin', 'orders', 'detail', id] as const
export const categoriesKey = ['admin', 'categories'] as const
export const productsKey = ['admin', 'products'] as const
export const inventoryKey = ['admin', 'inventory'] as const
export const purchasesKey = ['admin', 'purchases'] as const
export const expensesKey = ['admin', 'expenses'] as const
export const reportsKey = ['admin', 'reports'] as const
export const usersKey = ['admin', 'users'] as const
