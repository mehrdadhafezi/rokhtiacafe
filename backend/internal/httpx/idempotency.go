package httpx

import (
	"net/http"
	"regexp"

	"github.com/jackc/pgx/v5/pgtype"
)

// IdempotencyHeader is required on endpoints marked 🔁 in docs/API-CONTRACT.md.
const IdempotencyHeader = "Idempotency-Key"

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IdempotencyKey reads and validates the Idempotency-Key header (a UUID). On failure it writes
// 400 BAD_REQUEST and returns false.
func IdempotencyKey(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	var key pgtype.UUID
	v := r.Header.Get(IdempotencyHeader)
	if !uuidRE.MatchString(v) || key.Scan(v) != nil {
		Error(w, http.StatusBadRequest, "BAD_REQUEST", "Idempotency-Key header must be a UUID")
		return key, false
	}
	return key, true
}
