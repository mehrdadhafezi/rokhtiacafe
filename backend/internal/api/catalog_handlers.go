package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/auth"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/catalog"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/httpx"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/opt"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
)

type categoryView struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	SortOrder int32     `json:"sort_order"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toCategory(c store.Category) categoryView {
	return categoryView{ID: c.ID, Name: c.Name, SortOrder: c.SortOrder, IsActive: c.IsActive,
		CreatedAt: c.CreatedAt.Time, UpdatedAt: c.UpdatedAt.Time}
}

type productView struct {
	ID                    int64     `json:"id"`
	CategoryID            int64     `json:"category_id"`
	Name                  string    `json:"name"`
	Description           *string   `json:"description"`
	PriceRial             int64     `json:"price_rial"`
	InventoryMode         string    `json:"inventory_mode"`
	LowStockThreshold     *int32    `json:"low_stock_threshold"`
	EstimatedUnitCostRial *int64    `json:"estimated_unit_cost_rial,omitempty"` // cost field: ADMIN only
	IsActive              bool      `json:"is_active"`
	IsAvailable           bool      `json:"is_available"`
	ImageURL              *string   `json:"image_url"`
	SortOrder             int32     `json:"sort_order"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

func toProduct(p store.Product, showCost bool) productView {
	v := productView{ID: p.ID, CategoryID: p.CategoryID, Name: p.Name, PriceRial: p.PriceRial,
		InventoryMode: p.InventoryMode, IsActive: p.IsActive, IsAvailable: p.IsAvailable, SortOrder: p.SortOrder,
		CreatedAt: p.CreatedAt.Time, UpdatedAt: p.UpdatedAt.Time}
	if p.Description.Valid {
		d := p.Description.String
		v.Description = &d
	}
	if p.LowStockThreshold.Valid {
		t := p.LowStockThreshold.Int32
		v.LowStockThreshold = &t
	}
	if showCost && p.EstimatedUnitCostRial.Valid {
		c := p.EstimatedUnitCostRial.Int64
		v.EstimatedUnitCostRial = &c
	}
	if p.ImagePath.Valid {
		u := catalog.ImageURL(p.ImagePath.String)
		v.ImageURL = &u
	}
	return v
}

func canSeeCost(r *http.Request) bool {
	return auth.Can(auth.FromContext(r.Context()).Role, auth.ActInventoryViewCost)
}

func (a *API) writeCatalogError(w http.ResponseWriter, r *http.Request, err error) {
	var bad catalog.ValidationError
	switch {
	case errors.As(err, &bad):
		httpx.ValidationError(w, bad)
	case errors.Is(err, catalog.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "NOT_FOUND", "not found")
	case errors.Is(err, catalog.ErrNameTaken):
		httpx.Error(w, http.StatusConflict, "CONFLICT", "name already exists")
	case errors.Is(err, catalog.ErrStockNotEmpty):
		httpx.Error(w, http.StatusConflict, "CONFLICT", "product still has stock or reservations")
	default:
		a.internal(w, r, err)
	}
}

// ---- categories ----

func (a *API) listCategories(w http.ResponseWriter, r *http.Request) {
	list, err := a.catalog.ListCategories(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	out := make([]categoryView, len(list))
	for i, c := range list {
		out[i] = toCategory(c)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out})
}

type categoryBody struct {
	Name      *string `json:"name"`
	SortOrder *int32  `json:"sort_order"`
	IsActive  *bool   `json:"is_active"`
}

func (a *API) createCategory(w http.ResponseWriter, r *http.Request) {
	var in categoryBody
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if in.IsActive != nil {
		httpx.ValidationError(w, map[string]string{"is_active": "unknown"})
		return
	}
	c, err := a.catalog.CreateCategory(r.Context(), a.actor(r), catalog.CategoryInput{Name: in.Name, SortOrder: in.SortOrder})
	if err != nil {
		a.writeCatalogError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, toCategory(c))
}

func (a *API) updateCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in categoryBody
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	c, err := a.catalog.UpdateCategory(r.Context(), a.actor(r), id, catalog.CategoryInput(in))
	if err != nil {
		a.writeCatalogError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toCategory(c))
}

// ---- products ----

type productBody struct {
	CategoryID            *int64            `json:"category_id"`
	Name                  *string           `json:"name"`
	Description           opt.Field[string] `json:"description"`
	PriceRial             *int64            `json:"price_rial"`
	InventoryMode         *string           `json:"inventory_mode"`
	LowStockThreshold     opt.Field[int32]  `json:"low_stock_threshold"`
	EstimatedUnitCostRial opt.Field[int64]  `json:"estimated_unit_cost_rial"`
	IsActive              *bool             `json:"is_active"`
	SortOrder             *int32            `json:"sort_order"`
}

func (b productBody) input() catalog.ProductInput { return catalog.ProductInput(b) }

func (a *API) listProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var catID *int64
	if v := q.Get("category_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			httpx.ValidationError(w, map[string]string{"category_id": "invalid"})
			return
		}
		catID = &n
	}
	includeInactive := q.Get("include_inactive") == "true" || q.Get("include_inactive") == "1"
	list, err := a.catalog.ListProducts(r.Context(), catID, includeInactive)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	showCost := canSeeCost(r)
	out := make([]productView, len(list))
	for i, p := range list {
		out[i] = toProduct(p, showCost)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": out})
}

func (a *API) getProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := a.catalog.GetProduct(r.Context(), id)
	if err != nil {
		a.writeCatalogError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toProduct(p, canSeeCost(r)))
}

func (a *API) createProduct(w http.ResponseWriter, r *http.Request) {
	var in productBody
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	p, err := a.catalog.CreateProduct(r.Context(), a.actor(r), in.input())
	if err != nil {
		a.writeCatalogError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, toProduct(p, true))
}

func (a *API) updateProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in productBody
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	p, err := a.catalog.UpdateProduct(r.Context(), a.actor(r), id, in.input())
	if err != nil {
		a.writeCatalogError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toProduct(p, true))
}

func (a *API) setAvailability(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		IsAvailable *bool `json:"is_available"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if in.IsAvailable == nil {
		httpx.ValidationError(w, map[string]string{"is_available": "required"})
		return
	}
	p, err := a.catalog.SetAvailability(r.Context(), a.actor(r), id, *in.IsAvailable)
	if err != nil {
		a.writeCatalogError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toProduct(p, canSeeCost(r)))
}

// PUT /admin/products/{id}/image — multipart field "file".
func (a *API) putProductImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, catalog.MaxImageBytes+64<<10)
	file, _, err := r.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpx.Error(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "image must be at most 3 MB")
			return
		}
		httpx.ValidationError(w, map[string]string{"file": "required (multipart/form-data)"})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, catalog.MaxImageBytes+1))
	if err != nil {
		a.internal(w, r, err)
		return
	}
	jpeg, err := catalog.NormalizeImage(data)
	switch {
	case errors.Is(err, catalog.ErrImageTooLarge):
		httpx.Error(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "image must be at most 3 MB")
		return
	case errors.Is(err, catalog.ErrUnsupportedImage):
		httpx.Error(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "image must be JPEG, PNG or WebP")
		return
	case errors.Is(err, catalog.ErrImageDimensions):
		httpx.ValidationError(w, map[string]string{"file": "at most 4000x4000 pixels"})
		return
	case err != nil:
		a.internal(w, r, err)
		return
	}
	p, err := a.catalog.SetImage(r.Context(), a.actor(r), id, jpeg)
	if err != nil {
		a.writeCatalogError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toProduct(p, true))
}

func (a *API) deleteProductImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := a.catalog.RemoveImage(r.Context(), a.actor(r), id)
	if err != nil {
		a.writeCatalogError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, toProduct(p, true))
}

// ---- public menu ----

type orderingView struct {
	Open     bool   `json:"open"`
	OpensAt  string `json:"opens_at"`
	ClosesAt string `json:"closes_at"`
}

// GET /public/menu — no auth. Revalidated on every load (no-cache + ETag) so prices are never stale.
func (a *API) publicMenu(w http.ResponseWriter, r *http.Request) {
	cats, err := a.catalog.Menu(r.Context())
	if err != nil {
		a.internal(w, r, err)
		return
	}
	body, err := json.Marshal(map[string]any{
		"ordering": orderingView{
			Open:    a.orderingOpen(time.Now()),
			OpensAt: a.d.Ordering.Opens(), ClosesAt: a.d.Ordering.Closes(),
		},
		"categories": cats,
	})
	if err != nil {
		a.internal(w, r, err)
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match != "" && bytes.Contains([]byte(match), []byte(etag)) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(body, '\n'))
}

// publicRateLimit applies the general public per-IP limit (docs/SECURITY.md §5).
func (a *API) publicRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.d.PublicPerIP != nil {
			if ok, wait := a.d.PublicPerIP.Allow(a.clientIP(r).String()); !ok {
				rateLimited(w, wait)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// orderingOpen combines ordering hours with the staff kill switch.
func (a *API) orderingOpen(now time.Time) bool {
	if a.d.Orders != nil {
		return a.d.Orders.IsOpen(now)
	}
	return a.d.Ordering.IsOpen(now, a.d.Location)
}
