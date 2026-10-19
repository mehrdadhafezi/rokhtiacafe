// Package api assembles the /api/v1 routes and their authorization (docs/API-CONTRACT.md).
package api

import (
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/auth"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/catalog"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/config"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/ratelimit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/users"
)

// Deps are the collaborators the API needs.
type Deps struct {
	Pool           *pgxpool.Pool
	Logger         *slog.Logger
	Auth           *auth.Middleware
	TrustedProxies []netip.Prefix
	LoginPerIP     *ratelimit.Limiter
	LoginPerUser   *ratelimit.Limiter
	PublicPerIP    *ratelimit.Limiter // general public limit; nil disables it
	Images         *catalog.ImageStore
	Ordering       config.OrderingHours
	Location       *time.Location
}

// API is the /api/v1 handler plus the route→permission table used by the coverage test.
type API struct {
	d       Deps
	q       *store.Queries
	users   *users.Service
	catalog *catalog.Service
	handler http.Handler
	perms   map[string]auth.Action // "METHOD /api/v1/admin/..." → action
}

// New builds the router.
func New(d Deps) *API {
	a := &API{d: d, q: store.New(d.Pool), users: &users.Service{Pool: d.Pool},
		catalog: &catalog.Service{Pool: d.Pool, Images: d.Images}, perms: map[string]auth.Action{}}

	r := chi.NewRouter()
	r.Use(d.Auth.Authenticate)

	r.Route("/public", func(r chi.Router) {
		r.Use(a.publicRateLimit)
		r.Get("/menu", a.publicMenu)
	})

	r.Route("/auth", func(r chi.Router) {
		r.With(d.Auth.RequireOrigin).Post("/login", a.login)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAuth, d.Auth.RequireCSRF)
			r.Get("/me", a.me)
			r.Post("/logout", a.logout)
			r.With(auth.Require(auth.ActOwnPassword)).Post("/password", a.changePassword)
		})
	})

	r.Route("/admin", func(r chi.Router) {
		r.Use(auth.RequireAuth, d.Auth.RequireCSRF)
		g := guarded{r: r, prefix: "/api/v1/admin", perms: a.perms}
		g.handle(http.MethodGet, "/users", auth.ActUsersManage, a.listUsers)
		g.handle(http.MethodPost, "/users", auth.ActUsersManage, a.createUser)
		g.handle(http.MethodPatch, "/users/{id}", auth.ActUsersManage, a.updateUser)
		g.handle(http.MethodPost, "/users/{id}/password", auth.ActUsersManage, a.resetPassword)
		g.handle(http.MethodGet, "/audit-logs", auth.ActAuditView, a.listAuditLogs)

		g.handle(http.MethodGet, "/categories", auth.ActCatalogView, a.listCategories)
		g.handle(http.MethodPost, "/categories", auth.ActCatalogManage, a.createCategory)
		g.handle(http.MethodPatch, "/categories/{id}", auth.ActCatalogManage, a.updateCategory)
		g.handle(http.MethodGet, "/products", auth.ActCatalogView, a.listProducts)
		g.handle(http.MethodPost, "/products", auth.ActCatalogManage, a.createProduct)
		g.handle(http.MethodGet, "/products/{id}", auth.ActCatalogView, a.getProduct)
		g.handle(http.MethodPatch, "/products/{id}", auth.ActCatalogManage, a.updateProduct)
		g.handle(http.MethodPatch, "/products/{id}/availability", auth.ActProductAvailability, a.setAvailability)
		g.handle(http.MethodPut, "/products/{id}/image", auth.ActCatalogManage, a.putProductImage)
		g.handle(http.MethodDelete, "/products/{id}/image", auth.ActCatalogManage, a.deleteProductImage)
	})

	a.handler = r
	return a
}

// Handler is mounted at /api/v1.
func (a *API) Handler() http.Handler { return a.handler }

// Permissions returns the declared action for every admin route.
func (a *API) Permissions() map[string]auth.Action { return a.perms }

// guarded registers admin routes so each one is bound to exactly one policy action.
type guarded struct {
	r      chi.Router
	prefix string
	perms  map[string]auth.Action
}

func (g guarded) handle(method, pattern string, act auth.Action, h http.HandlerFunc) {
	if !auth.Known(act) {
		panic("api: route " + method + " " + pattern + " uses unknown action " + string(act))
	}
	g.perms[method+" "+g.prefix+pattern] = act
	g.r.With(auth.Require(act)).Method(method, pattern, h)
}

func (a *API) clientIP(r *http.Request) netip.Addr {
	return httpx.ClientIP(r, a.d.TrustedProxies)
}
