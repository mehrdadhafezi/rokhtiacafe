// Package audit writes audit_logs rows inside the caller's transaction (docs/DATABASE.md §2.14).
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

// Actor kinds.
const (
	KindUser     = "USER"
	KindCustomer = "CUSTOMER"
	KindSystem   = "SYSTEM"
)

// Actor identifies who performed an action. UserID 0 with Kind SYSTEM is a CLI or job.
type Actor struct {
	UserID int64
	Kind   string
	IP     netip.Addr
}

// System is the actor for CLI commands and background jobs.
var System = Actor{Kind: KindSystem}

// UserActor is a logged-in staff member.
func UserActor(userID int64, ip netip.Addr) Actor {
	return Actor{UserID: userID, Kind: KindUser, IP: ip}
}

// Entry is one audited action. Details must never contain secrets (passwords, tokens, hashes).
type Entry struct {
	Action     string // e.g. "user.create"
	EntityType string
	EntityID   string
	Details    map[string]any
}

// Record inserts the audit row using q, which should be bound to the same transaction as the change.
func Record(ctx context.Context, q store.Querier, a Actor, e Entry) error {
	details := []byte("{}")
	if len(e.Details) > 0 {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return fmt.Errorf("audit details: %w", err)
		}
		details = b
	}
	var ip *netip.Addr
	if a.IP.IsValid() {
		ip = &a.IP
	}
	return q.InsertAuditLog(ctx, store.InsertAuditLogParams{
		ActorUserID: pgtype.Int8{Int64: a.UserID, Valid: a.Kind == KindUser},
		ActorKind:   a.Kind,
		Action:      e.Action,
		EntityType:  e.EntityType,
		EntityID:    e.EntityID,
		Details:     details,
		Ip:          ip,
	})
}
