// Package catalog manages categories and products (docs/API-CONTRACT.md §3, §5.4).
// Products are never deleted; deactivation hides them from the menu.
package catalog

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/audit"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/opt"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/store"
	"github.com/mehrdadhafezi/rokhtiacafe/backend/internal/textutil"
)

const (
	ModeTracked   = "TRACKED"
	ModeUntracked = "UNTRACKED"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrNameTaken     = errors.New("name already exists")
	ErrStockNotEmpty = errors.New("product still has stock or reservations")
)

// ValidationError lists invalid fields.
type ValidationError map[string]string

func (v ValidationError) Error() string { return "invalid catalog input" }

// Service holds catalog operations. Every mutation is audited in its transaction.
type Service struct {
	Pool   *pgxpool.Pool
	Images *ImageStore
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func id(v int64) string { return strconv.FormatInt(v, 10) }

// ---- categories ----

// CategoryInput is used for create (Name required) and update (nil = unchanged).
type CategoryInput struct {
	Name      *string
	SortOrder *int32
	IsActive  *bool
}

func cleanCategoryName(p *string, bad ValidationError) string {
	if p == nil {
		return ""
	}
	s := textutil.Line(*p)
	if n := textutil.Len(s); n < 1 || n > 60 {
		bad["name"] = "length"
	}
	return s
}

// ListCategories returns every category, including inactive ones.
func (s *Service) ListCategories(ctx context.Context) ([]store.Category, error) {
	return store.New(s.Pool).ListCategories(ctx)
}

// CreateCategory adds a category.
func (s *Service) CreateCategory(ctx context.Context, actor audit.Actor, in CategoryInput) (store.Category, error) {
	bad := ValidationError{}
	if in.Name == nil {
		bad["name"] = "required"
	}
	name := cleanCategoryName(in.Name, bad)
	if len(bad) > 0 {
		return store.Category{}, bad
	}
	var sort int32
	if in.SortOrder != nil {
		sort = *in.SortOrder
	}
	var c store.Category
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		var err error
		c, err = q.CreateCategory(ctx, store.CreateCategoryParams{Name: name, SortOrder: sort})
		if isUnique(err) {
			return ErrNameTaken
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "category.create", EntityType: "category",
			EntityID: id(c.ID), Details: map[string]any{"name": c.Name}})
	})
	return c, err
}

// UpdateCategory renames, reorders or (de)activates a category.
func (s *Service) UpdateCategory(ctx context.Context, actor audit.Actor, catID int64, in CategoryInput) (store.Category, error) {
	bad := ValidationError{}
	name := cleanCategoryName(in.Name, bad)
	if len(bad) > 0 {
		return store.Category{}, bad
	}
	var c store.Category
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		old, err := q.GetCategoryForUpdate(ctx, catID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		next := store.UpdateCategoryParams{ID: catID, Name: old.Name, SortOrder: old.SortOrder, IsActive: old.IsActive}
		if in.Name != nil {
			next.Name = name
		}
		if in.SortOrder != nil {
			next.SortOrder = *in.SortOrder
		}
		if in.IsActive != nil {
			next.IsActive = *in.IsActive
		}
		c, err = q.UpdateCategory(ctx, next)
		if isUnique(err) {
			return ErrNameTaken
		}
		if err != nil {
			return err
		}
		changes := map[string]any{}
		if old.Name != c.Name {
			changes["name"] = []string{old.Name, c.Name}
		}
		if old.SortOrder != c.SortOrder {
			changes["sort_order"] = []int32{old.SortOrder, c.SortOrder}
		}
		if old.IsActive != c.IsActive {
			changes["is_active"] = []bool{old.IsActive, c.IsActive}
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "category.update", EntityType: "category",
			EntityID: id(catID), Details: changes})
	})
	return c, err
}

// ---- products ----

// ProductInput is used for create and update. For update, unset fields keep their value and
// opt.Null clears an optional field.
type ProductInput struct {
	CategoryID            *int64
	Name                  *string
	Description           opt.Field[string]
	PriceRial             *int64
	InventoryMode         *string
	LowStockThreshold     opt.Field[int32]
	EstimatedUnitCostRial opt.Field[int64]
	IsActive              *bool
	SortOrder             *int32
}

// ListProducts returns products, optionally filtered by category and including inactive ones.
func (s *Service) ListProducts(ctx context.Context, categoryID *int64, includeInactive bool) ([]store.Product, error) {
	p := store.ListProductsParams{IncludeInactive: includeInactive}
	if categoryID != nil {
		p.CategoryID = pgtype.Int8{Int64: *categoryID, Valid: true}
	}
	return store.New(s.Pool).ListProducts(ctx, p)
}

// GetProduct loads one product.
func (s *Service) GetProduct(ctx context.Context, productID int64) (store.Product, error) {
	p, err := store.New(s.Pool).GetProduct(ctx, productID)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// productState is the fully merged state validated before writing.
type productState struct {
	categoryID int64
	name       string
	desc       pgtype.Text
	price      int64
	mode       string
	threshold  pgtype.Int4
	estimate   pgtype.Int8
	isActive   bool
	sortOrder  int32
}

func fromProduct(p store.Product) productState {
	return productState{categoryID: p.CategoryID, name: p.Name, desc: p.Description, price: p.PriceRial,
		mode: p.InventoryMode, threshold: p.LowStockThreshold, estimate: p.EstimatedUnitCostRial,
		isActive: p.IsActive, sortOrder: p.SortOrder}
}

// apply merges in onto st and validates the result. A mode switch clears the field that the new
// mode does not allow unless the caller set it explicitly (which is then a validation error).
func (st productState) apply(in ProductInput) (productState, ValidationError) {
	bad := ValidationError{}
	if in.CategoryID != nil {
		st.categoryID = *in.CategoryID
	}
	if in.Name != nil {
		st.name = textutil.Line(*in.Name)
	}
	if in.Description.Set {
		if in.Description.Null {
			st.desc = pgtype.Text{}
		} else {
			d := textutil.Multiline(in.Description.Value)
			st.desc = pgtype.Text{String: d, Valid: d != ""}
		}
	}
	if in.PriceRial != nil {
		st.price = *in.PriceRial
	}
	modeChanged := false
	if in.InventoryMode != nil {
		modeChanged = *in.InventoryMode != st.mode
		st.mode = *in.InventoryMode
	}
	if in.LowStockThreshold.Set {
		st.threshold = pgtype.Int4{Int32: in.LowStockThreshold.Value, Valid: !in.LowStockThreshold.Null}
	} else if modeChanged && st.mode == ModeUntracked {
		st.threshold = pgtype.Int4{}
	}
	if in.EstimatedUnitCostRial.Set {
		st.estimate = pgtype.Int8{Int64: in.EstimatedUnitCostRial.Value, Valid: !in.EstimatedUnitCostRial.Null}
	} else if modeChanged && st.mode == ModeTracked {
		st.estimate = pgtype.Int8{}
	}
	if in.IsActive != nil {
		st.isActive = *in.IsActive
	}
	if in.SortOrder != nil {
		st.sortOrder = *in.SortOrder
	}

	if st.categoryID <= 0 {
		bad["category_id"] = "required"
	}
	if n := textutil.Len(st.name); n < 1 || n > 80 {
		bad["name"] = "length"
	}
	if st.desc.Valid && textutil.Len(st.desc.String) > 300 {
		bad["description"] = "length"
	}
	if st.price <= 0 || st.price%10 != 0 {
		bad["price_rial"] = "positive whole tomans (multiple of 10 rials)"
	}
	switch st.mode {
	case ModeTracked:
		if st.estimate.Valid {
			bad["estimated_unit_cost_rial"] = "only for UNTRACKED products"
		}
		if st.threshold.Valid && st.threshold.Int32 < 0 {
			bad["low_stock_threshold"] = "must be >= 0"
		}
	case ModeUntracked:
		if st.threshold.Valid {
			bad["low_stock_threshold"] = "only for TRACKED products"
		}
		if st.estimate.Valid && st.estimate.Int64 < 0 {
			bad["estimated_unit_cost_rial"] = "must be >= 0"
		}
	default:
		bad["inventory_mode"] = "TRACKED or UNTRACKED"
	}
	return st, bad
}

func checkCategory(ctx context.Context, q *store.Queries, catID int64) error {
	if _, err := q.GetCategory(ctx, catID); errors.Is(err, pgx.ErrNoRows) {
		return ValidationError{"category_id": "unknown"}
	} else if err != nil {
		return err
	}
	return nil
}

// CreateProduct adds a product; TRACKED products get their inventory balance row.
func (s *Service) CreateProduct(ctx context.Context, actor audit.Actor, in ProductInput) (store.Product, error) {
	bad := ValidationError{}
	if in.CategoryID == nil {
		bad["category_id"] = "required"
	}
	if in.Name == nil {
		bad["name"] = "required"
	}
	if in.PriceRial == nil {
		bad["price_rial"] = "required"
	}
	if in.InventoryMode == nil {
		bad["inventory_mode"] = "required"
	}
	if in.IsActive != nil && !*in.IsActive {
		bad["is_active"] = "new products start active"
	}
	if len(bad) > 0 {
		return store.Product{}, bad
	}
	st, bad := productState{isActive: true}.apply(in)
	if len(bad) > 0 {
		return store.Product{}, bad
	}

	var p store.Product
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		if err := checkCategory(ctx, q, st.categoryID); err != nil {
			return err
		}
		var err error
		p, err = q.CreateProduct(ctx, store.CreateProductParams{
			CategoryID: st.categoryID, Name: st.name, Description: st.desc, PriceRial: st.price,
			InventoryMode: st.mode, LowStockThreshold: st.threshold, EstimatedUnitCostRial: st.estimate, SortOrder: st.sortOrder,
		})
		if isUnique(err) {
			return ErrNameTaken
		}
		if err != nil {
			return err
		}
		if p.InventoryMode == ModeTracked {
			if err := q.EnsureInventoryBalance(ctx, p.ID); err != nil {
				return err
			}
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "product.create", EntityType: "product", EntityID: id(p.ID),
			Details: map[string]any{"name": p.Name, "price_rial": p.PriceRial, "inventory_mode": p.InventoryMode}})
	})
	return p, err
}

// UpdateProduct edits a product. Switching TRACKED → UNTRACKED requires zero stock and no
// reservations; switching to TRACKED creates the balance row. Existing orders are unaffected
// because their lines carry snapshots.
func (s *Service) UpdateProduct(ctx context.Context, actor audit.Actor, productID int64, in ProductInput) (store.Product, error) {
	var p store.Product
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		old, err := q.GetProductForUpdate(ctx, productID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		st, bad := fromProduct(old).apply(in)
		if len(bad) > 0 {
			return bad
		}
		if st.categoryID != old.CategoryID {
			if err := checkCategory(ctx, q, st.categoryID); err != nil {
				return err
			}
		}
		if old.InventoryMode == ModeTracked && st.mode == ModeUntracked {
			b, err := q.GetInventoryBalanceForUpdate(ctx, productID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil && (b.OnHand != 0 || b.Reserved != 0) {
				return ErrStockNotEmpty
			}
		}
		p, err = q.UpdateProduct(ctx, store.UpdateProductParams{
			ID: productID, CategoryID: st.categoryID, Name: st.name, Description: st.desc, PriceRial: st.price,
			InventoryMode: st.mode, LowStockThreshold: st.threshold, EstimatedUnitCostRial: st.estimate,
			IsActive: st.isActive, SortOrder: st.sortOrder,
		})
		if isUnique(err) {
			return ErrNameTaken
		}
		if err != nil {
			return err
		}
		if p.InventoryMode == ModeTracked {
			if err := q.EnsureInventoryBalance(ctx, p.ID); err != nil {
				return err
			}
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "product.update", EntityType: "product",
			EntityID: id(productID), Details: productChanges(old, p)})
	})
	return p, err
}

func productChanges(old, p store.Product) map[string]any {
	c := map[string]any{}
	if old.CategoryID != p.CategoryID {
		c["category_id"] = []int64{old.CategoryID, p.CategoryID}
	}
	if old.Name != p.Name {
		c["name"] = []string{old.Name, p.Name}
	}
	if old.Description != p.Description {
		c["description"] = "changed"
	}
	if old.PriceRial != p.PriceRial {
		c["price_rial"] = []int64{old.PriceRial, p.PriceRial}
	}
	if old.InventoryMode != p.InventoryMode {
		c["inventory_mode"] = []string{old.InventoryMode, p.InventoryMode}
	}
	if old.LowStockThreshold != p.LowStockThreshold {
		c["low_stock_threshold"] = []any{nullable(old.LowStockThreshold.Int32, old.LowStockThreshold.Valid), nullable(p.LowStockThreshold.Int32, p.LowStockThreshold.Valid)}
	}
	if old.EstimatedUnitCostRial != p.EstimatedUnitCostRial {
		c["estimated_unit_cost_rial"] = []any{nullable(old.EstimatedUnitCostRial.Int64, old.EstimatedUnitCostRial.Valid), nullable(p.EstimatedUnitCostRial.Int64, p.EstimatedUnitCostRial.Valid)}
	}
	if old.IsActive != p.IsActive {
		c["is_active"] = []bool{old.IsActive, p.IsActive}
	}
	if old.SortOrder != p.SortOrder {
		c["sort_order"] = []int32{old.SortOrder, p.SortOrder}
	}
	return c
}

func nullable[T any](v T, ok bool) any {
	if !ok {
		return nil
	}
	return v
}

// SetAvailability toggles the sold-out flag (OPERATOR allowed).
func (s *Service) SetAvailability(ctx context.Context, actor audit.Actor, productID int64, available bool) (store.Product, error) {
	var p store.Product
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		old, err := q.GetProductForUpdate(ctx, productID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		p, err = q.SetProductAvailability(ctx, store.SetProductAvailabilityParams{ID: productID, IsAvailable: available})
		if err != nil {
			return err
		}
		if old.IsAvailable == available {
			return nil
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "product.availability", EntityType: "product",
			EntityID: id(productID), Details: map[string]any{"is_available": []bool{old.IsAvailable, available}}})
	})
	return p, err
}

// SetImage stores an already-normalized JPEG and points the product at it. The previous file is
// removed only after the database change commits.
func (s *Service) SetImage(ctx context.Context, actor audit.Actor, productID int64, jpeg []byte) (store.Product, error) {
	name, err := s.Images.Save(jpeg)
	if err != nil {
		return store.Product{}, err
	}
	var p, old store.Product
	err = store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		var err error
		old, err = q.GetProductForUpdate(ctx, productID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		p, err = q.SetProductImage(ctx, store.SetProductImageParams{ID: productID, ImagePath: pgtype.Text{String: name, Valid: true}})
		if err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "product.image_set", EntityType: "product", EntityID: id(productID)})
	})
	if err != nil {
		s.Images.Remove(name)
		return store.Product{}, err
	}
	if old.ImagePath.Valid {
		s.Images.Remove(old.ImagePath.String)
	}
	return p, nil
}

// RemoveImage clears the product image.
func (s *Service) RemoveImage(ctx context.Context, actor audit.Actor, productID int64) (store.Product, error) {
	var p, old store.Product
	err := store.InTx(ctx, s.Pool, func(q *store.Queries) error {
		var err error
		old, err = q.GetProductForUpdate(ctx, productID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		p, err = q.SetProductImage(ctx, store.SetProductImageParams{ID: productID})
		if err != nil || !old.ImagePath.Valid {
			return err
		}
		return audit.Record(ctx, q, actor, audit.Entry{Action: "product.image_remove", EntityType: "product", EntityID: id(productID)})
	})
	if err == nil && old.ImagePath.Valid {
		s.Images.Remove(old.ImagePath.String)
	}
	return p, err
}

// MenuItem and MenuCategory are the public menu shape.
type MenuItem struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	PriceRial   int64   `json:"price_rial"`
	ImageURL    *string `json:"image_url"`
	Available   bool    `json:"available"`
}

type MenuCategory struct {
	ID       int64      `json:"id"`
	Name     string     `json:"name"`
	Products []MenuItem `json:"products"`
}

// Menu returns active categories with their active products. Categories without products are omitted.
func (s *Service) Menu(ctx context.Context) ([]MenuCategory, error) {
	rows, err := store.New(s.Pool).PublicMenu(ctx)
	if err != nil {
		return nil, err
	}
	out := []MenuCategory{}
	for _, r := range rows {
		if len(out) == 0 || out[len(out)-1].ID != r.CategoryID {
			out = append(out, MenuCategory{ID: r.CategoryID, Name: r.CategoryName, Products: []MenuItem{}})
		}
		item := MenuItem{ID: r.ProductID, Name: r.Name, PriceRial: r.PriceRial, Available: r.Available}
		if r.Description.Valid {
			d := r.Description.String
			item.Description = &d
		}
		if r.ImagePath.Valid {
			u := ImageURL(r.ImagePath.String)
			item.ImageURL = &u
		}
		out[len(out)-1].Products = append(out[len(out)-1].Products, item)
	}
	return out, nil
}
