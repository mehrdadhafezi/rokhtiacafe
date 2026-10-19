-- name: ListCategories :many
SELECT * FROM categories ORDER BY sort_order, id;

-- name: GetCategory :one
SELECT * FROM categories WHERE id = $1;

-- name: GetCategoryForUpdate :one
SELECT * FROM categories WHERE id = $1 FOR UPDATE;

-- name: CreateCategory :one
INSERT INTO categories (name, sort_order) VALUES (@name, @sort_order) RETURNING *;

-- name: UpdateCategory :one
UPDATE categories SET name = @name, sort_order = @sort_order, is_active = @is_active
WHERE id = @id
RETURNING *;

-- name: ListProducts :many
SELECT * FROM products
WHERE (sqlc.narg('category_id')::int8 IS NULL OR category_id = sqlc.narg('category_id'))
  AND (@include_inactive::bool OR is_active)
ORDER BY category_id, sort_order, id;

-- name: GetProduct :one
SELECT * FROM products WHERE id = $1;

-- name: GetProductForUpdate :one
SELECT * FROM products WHERE id = $1 FOR UPDATE;

-- name: CreateProduct :one
INSERT INTO products (category_id, name, description, price_rial, inventory_mode,
                      low_stock_threshold, estimated_unit_cost_rial, sort_order)
VALUES (@category_id, @name, @description, @price_rial, @inventory_mode,
        @low_stock_threshold, @estimated_unit_cost_rial, @sort_order)
RETURNING *;

-- name: UpdateProduct :one
UPDATE products SET
  category_id = @category_id, name = @name, description = @description, price_rial = @price_rial,
  inventory_mode = @inventory_mode, low_stock_threshold = @low_stock_threshold,
  estimated_unit_cost_rial = @estimated_unit_cost_rial, is_active = @is_active, sort_order = @sort_order
WHERE id = @id
RETURNING *;

-- name: SetProductAvailability :one
UPDATE products SET is_available = @is_available WHERE id = @id RETURNING *;

-- name: SetProductImage :one
UPDATE products SET image_path = @image_path WHERE id = @id RETURNING *;

-- name: EnsureInventoryBalance :exec
-- Every TRACKED product has a balance row (created with the product or on a mode switch).
INSERT INTO inventory_balances (product_id) VALUES ($1) ON CONFLICT (product_id) DO NOTHING;

-- name: GetInventoryBalanceForUpdate :one
SELECT * FROM inventory_balances WHERE product_id = $1 FOR UPDATE;

-- name: PublicMenu :many
-- Active categories and products only. Exact stock is never exposed, only availability.
SELECT c.id AS category_id, c.name AS category_name,
       p.id AS product_id, p.name, p.description, p.price_rial, p.image_path,
       (p.is_available AND (p.inventory_mode = 'UNTRACKED' OR COALESCE(b.on_hand - b.reserved, 0) > 0))::bool AS available
FROM categories c
JOIN products p ON p.category_id = c.id
LEFT JOIN inventory_balances b ON b.product_id = p.id
WHERE c.is_active AND p.is_active
ORDER BY c.sort_order, c.id, p.sort_order, p.id;
