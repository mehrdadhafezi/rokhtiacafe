// Package users manages staff accounts (docs/API-CONTRACT.md §5.9).
package users

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/auth"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

var (
	ErrNotFound      = errors.New("user not found")
	ErrUsernameTaken = errors.New("username already exists")
	ErrLastAdmin     = errors.New("at least one active ADMIN must remain")
)

// ValidationError lists invalid fields.
type ValidationError map[string]string

func (v ValidationError) Error() string { return "invalid user input" }

var usernameRE = regexp.MustCompile(`^[a-z0-9_.]{3,32}$`)

// Service holds the user operations. All mutations are audited in the same transaction.
type Service struct {
	Pool *pgxpool.Pool
}

// CreateInput is a new account.
type CreateInput struct {
	Username    string
	DisplayName string
	Role        string
	Password    string
}

// UpdateInput changes profile fields; nil means unchanged.
type UpdateInput struct {
	DisplayName *string
	Role        *string
	IsActive    *bool
}

// NormalizeUsername trims and lower-cases a username.
func NormalizeUsername(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func validateDisplayName(s string) bool {
	n := utf8.RuneCountInString(s)
	return n >= 1 && n <= 60
}

// List returns all users ordered by id.
func (s *Service) List(ctx context.Context) ([]store.User, error) {
	return store.New(s.Pool).ListUsers(ctx)
}

// Create validates, hashes the password and inserts the user.
func (s *Service) Create(ctx context.Context, actor audit.Actor, in CreateInput) (store.User, error) {
	in.Username = NormalizeUsername(in.Username)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	bad := ValidationError{}
	if !usernameRE.MatchString(in.Username) {
		bad["username"] = "format"
	}
	if !validateDisplayName(in.DisplayName) {
		bad["display_name"] = "length"
	}
	if !auth.Role(in.Role).Valid() {
		bad["role"] = "invalid"
	}
	if err := auth.ValidatePassword(in.Password); err != nil {
		bad["password"] = "policy"
	}
	if len(bad) > 0 {
		return store.User{}, bad
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return store.User{}, err
	}

	var u store.User
	err = store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		if err := q.AdvisoryXactLock(ctx, store.LockAdminMembership); err != nil {
			return err
		}
		u, err = q.CreateUser(ctx, store.CreateUserParams{
			Username: in.Username, DisplayName: in.DisplayName, PasswordHash: hash, Role: in.Role,
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrUsernameTaken
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{
			Action: "user.create", EntityType: "user", EntityID: strconv.FormatInt(u.ID, 10),
			Details: map[string]any{"username": u.Username, "role": u.Role},
		})
	})
	return u, err
}

// Update applies profile changes. Role changes and deactivation keep at least one active ADMIN
// (checked under LOCK_ADMIN_MEMBERSHIP) and revoke the user's sessions.
func (s *Service) Update(ctx context.Context, actor audit.Actor, id int64, in UpdateInput) (store.User, error) {
	bad := ValidationError{}
	if in.DisplayName != nil {
		v := strings.TrimSpace(*in.DisplayName)
		in.DisplayName = &v
		if !validateDisplayName(v) {
			bad["display_name"] = "length"
		}
	}
	if in.Role != nil && !auth.Role(*in.Role).Valid() {
		bad["role"] = "invalid"
	}
	if len(bad) > 0 {
		return store.User{}, bad
	}

	var u store.User
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		if err := q.AdvisoryXactLock(ctx, store.LockAdminMembership); err != nil {
			return err
		}
		old, err := q.GetUserByIDForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		next := store.UpdateUserProfileParams{ID: id, DisplayName: old.DisplayName, Role: old.Role, IsActive: old.IsActive}
		if in.DisplayName != nil {
			next.DisplayName = *in.DisplayName
		}
		if in.Role != nil {
			next.Role = *in.Role
		}
		if in.IsActive != nil {
			next.IsActive = *in.IsActive
		}

		wasAdmin := old.IsActive && old.Role == string(auth.RoleAdmin)
		staysAdmin := next.IsActive && next.Role == string(auth.RoleAdmin)
		if wasAdmin && !staysAdmin {
			n, err := q.CountActiveAdmins(ctx)
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastAdmin
			}
		}

		u, err = q.UpdateUserProfile(ctx, next)
		if err != nil {
			return err
		}
		if old.Role != u.Role || (old.IsActive && !u.IsActive) {
			if err := q.DeleteUserSessions(ctx, id); err != nil {
				return err
			}
		}
		changes := map[string]any{}
		if old.DisplayName != u.DisplayName {
			changes["display_name"] = []string{old.DisplayName, u.DisplayName}
		}
		if old.Role != u.Role {
			changes["role"] = []string{old.Role, u.Role}
		}
		if old.IsActive != u.IsActive {
			changes["is_active"] = []bool{old.IsActive, u.IsActive}
		}
		return audit.Record(ctx, q, actor, audit.Entry{
			Action: "user.update", EntityType: "user", EntityID: strconv.FormatInt(id, 10), Details: changes,
		})
	})
	return u, err
}

// ResetPassword sets a new password (admin action) and revokes all of the user's sessions.
func (s *Service) ResetPassword(ctx context.Context, actor audit.Actor, id int64, password string) error {
	if err := auth.ValidatePassword(password); err != nil {
		return ValidationError{"new_password": "policy"}
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		if _, err := q.GetUserByIDForUpdate(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if err := q.SetUserPassword(ctx, store.SetUserPasswordParams{ID: id, PasswordHash: hash}); err != nil {
			return err
		}
		if err := q.DeleteUserSessions(ctx, id); err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{
			Action: "user.password_reset", EntityType: "user", EntityID: strconv.FormatInt(id, 10),
		})
	})
}
