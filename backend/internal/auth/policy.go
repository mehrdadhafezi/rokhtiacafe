package auth

// Role is a staff role (docs/SECURITY.md §3).
type Role string

const (
	RoleAdmin    Role = "ADMIN"
	RoleOperator Role = "OPERATOR"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleOperator }

// Action is a permission checked by Can. Every admin route declares exactly one.
type Action string

// The permission matrix from docs/SECURITY.md §3. Actions for modules that land later are
// defined now so the matrix is reviewed in one place.
const (
	ActDashboardView       Action = "dashboard.view"
	ActDashboardRevenue    Action = "dashboard.revenue"
	ActOrdersView          Action = "orders.view"
	ActOrdersTransition    Action = "orders.transition" // accept / ready / complete / cancel
	ActPaymentsRecord      Action = "payments.record"
	ActCancelWithRefund    Action = "orders.cancel_prepaid" // cancel an order holding a prepayment
	ActPaymentsRefund      Action = "payments.refund"       // refund a completed order
	ActPaymentsVoid        Action = "payments.void"
	ActOrderingToggle      Action = "ordering.toggle"
	ActCatalogView         Action = "catalog.view"
	ActProductAvailability Action = "catalog.availability"
	ActCatalogManage       Action = "catalog.manage"
	ActInventoryViewQty    Action = "inventory.view_qty"
	ActInventoryViewCost   Action = "inventory.view_cost"
	ActInventoryManage     Action = "inventory.manage" // purchases, adjustments, voids
	ActExpensesManage      Action = "expenses.manage"
	ActReportsView         Action = "reports.view"
	ActPushManage          Action = "push.manage"
	ActUsersManage         Action = "users.manage"
	ActAuditView           Action = "audit.view"
	ActOwnPassword         Action = "self.password"
)

var operatorAllowed = map[Action]bool{
	ActDashboardView:       true,
	ActOrdersView:          true,
	ActOrdersTransition:    true,
	ActPaymentsRecord:      true,
	ActCancelWithRefund:    true,
	ActCatalogView:         true,
	ActProductAvailability: true,
	ActInventoryViewQty:    true,
	ActOwnPassword:         true,
}

// AllActions lists every action in the matrix; ADMIN may perform all of them.
var AllActions = []Action{
	ActDashboardView, ActDashboardRevenue, ActOrdersView, ActOrdersTransition, ActPaymentsRecord,
	ActCancelWithRefund, ActPaymentsRefund, ActPaymentsVoid, ActOrderingToggle, ActCatalogView,
	ActProductAvailability, ActCatalogManage, ActInventoryViewQty, ActInventoryViewCost,
	ActInventoryManage, ActExpensesManage, ActReportsView, ActPushManage, ActUsersManage,
	ActAuditView, ActOwnPassword,
}

var knownActions = func() map[Action]bool {
	m := make(map[Action]bool, len(AllActions))
	for _, a := range AllActions {
		m[a] = true
	}
	return m
}()

// Known reports whether a is part of the matrix.
func Known(a Action) bool { return knownActions[a] }

// Can is the single authorization decision point.
func Can(role Role, a Action) bool {
	if !knownActions[a] {
		return false
	}
	switch role {
	case RoleAdmin:
		return true
	case RoleOperator:
		return operatorAllowed[a]
	default:
		return false
	}
}
