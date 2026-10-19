package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedCatalog creates two categories and three products through the API and returns their ids.
func seedCatalog(t *testing.T, admin *client) (drinks, snacks, espresso, soda, cake float64) {
	t.Helper()
	mk := func(path string, body map[string]any) map[string]any {
		resp, b := admin.do(http.MethodPost, path, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST %s %v: %d %v", path, body, resp.StatusCode, b)
		}
		return b
	}
	drinks = mk("/api/v1/admin/categories", map[string]any{"name": " نوشیدنی ", "sort_order": 1})["id"].(float64)
	snacks = mk("/api/v1/admin/categories", map[string]any{"name": "کیک و اسنک", "sort_order": 2})["id"].(float64)
	espresso = mk("/api/v1/admin/products", map[string]any{"category_id": drinks, "name": "اسپرسو", "price_rial": 950000,
		"inventory_mode": "UNTRACKED", "estimated_unit_cost_rial": 300000, "description": "دبل شات"})["id"].(float64)
	soda = mk("/api/v1/admin/products", map[string]any{"category_id": drinks, "name": "نوشابه", "price_rial": 450000,
		"inventory_mode": "TRACKED", "low_stock_threshold": 3})["id"].(float64)
	cake = mk("/api/v1/admin/products", map[string]any{"category_id": snacks, "name": "کیک شکلاتی", "price_rial": 1200000,
		"inventory_mode": "TRACKED"})["id"].(float64)
	return
}

func path(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func TestCatalogAdminCRUD(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	drinks, _, espresso, soda, _ := seedCatalog(t, admin)

	// Tracked products get a balance row; untracked do not.
	var n int
	_ = e.pool.QueryRow(e.ctx, "SELECT count(*) FROM inventory_balances").Scan(&n)
	if n != 2 {
		t.Errorf("balance rows = %d, want 2 (tracked products only)", n)
	}

	resp, body := admin.do(http.MethodPost, "/api/v1/admin/categories", map[string]any{"name": "نوشیدنی"})
	expect(t, resp, body, http.StatusConflict, "CONFLICT")
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/products", map[string]any{"category_id": drinks, "name": "اسپرسو", "price_rial": 10, "inventory_mode": "UNTRACKED"})
	expect(t, resp, body, http.StatusConflict, "CONFLICT")

	resp, body = admin.do(http.MethodPost, "/api/v1/admin/products", map[string]any{
		"category_id": 999, "name": "", "price_rial": 12345, "inventory_mode": "UNTRACKED", "low_stock_threshold": 2})
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	fields := body["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)
	for _, f := range []string{"name", "price_rial", "low_stock_threshold"} {
		if fields[f] == nil {
			t.Errorf("expected validation error on %s: %v", f, fields)
		}
	}
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/products", map[string]any{"category_id": 999, "name": "x", "price_rial": 10, "inventory_mode": "TRACKED"})
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/products", map[string]any{"category_id": drinks, "name": "x", "price_rial": 10, "inventory_mode": "TRACKED", "estimated_unit_cost_rial": 5})
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	resp, body = admin.do(http.MethodPost, "/api/v1/admin/products", map[string]any{"category_id": drinks, "name": "x", "price_rial": 10, "inventory_mode": "TRACKED", "price": 1})
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR") // unknown field

	// Price change is audited with old and new values; description can be cleared with null.
	resp, body = admin.do(http.MethodPatch, path("/api/v1/admin/products/%v", espresso), map[string]any{"price_rial": 1000000, "description": nil})
	expect(t, resp, body, http.StatusOK, "")
	if body["price_rial"].(float64) != 1000000 || body["description"] != nil || body["estimated_unit_cost_rial"].(float64) != 300000 {
		t.Errorf("patched = %v", body)
	}
	var details string
	_ = e.pool.QueryRow(e.ctx, `SELECT details::text FROM audit_logs WHERE action = 'product.update' ORDER BY id DESC LIMIT 1`).Scan(&details)
	if !strings.Contains(details, `"price_rial": [950000, 1000000]`) {
		t.Errorf("price change audit = %s", details)
	}

	resp, body = admin.do(http.MethodPatch, path("/api/v1/admin/categories/%v", drinks), map[string]any{"is_active": false})
	expect(t, resp, body, http.StatusOK, "")
	resp, body = admin.do(http.MethodGet, path("/api/v1/admin/products/%v", soda), nil)
	expect(t, resp, body, http.StatusOK, "")
	resp, body = admin.do(http.MethodGet, "/api/v1/admin/products/987654", nil)
	expect(t, resp, body, http.StatusNotFound, "NOT_FOUND")

	resp, body = admin.do(http.MethodPatch, path("/api/v1/admin/products/%v", soda), map[string]any{"is_active": false})
	expect(t, resp, body, http.StatusOK, "")
	resp, body = admin.do(http.MethodGet, path("/api/v1/admin/products?category_id=%v", drinks), nil)
	if items := body["items"].([]any); len(items) != 1 {
		t.Errorf("active drinks = %d, want 1", len(items))
	}
	resp, body = admin.do(http.MethodGet, path("/api/v1/admin/products?category_id=%v&include_inactive=true", drinks), nil)
	if items := body["items"].([]any); len(items) != 2 {
		t.Errorf("all drinks = %d, want 2", len(items))
	}
}

func TestInventoryModeSwitchRules(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, _, soda, _ := seedCatalog(t, admin)

	if _, err := e.pool.Exec(e.ctx, `UPDATE inventory_balances SET on_hand = 4, inventory_value_rial = 400 WHERE product_id = $1`, int64(soda)); err != nil {
		t.Fatal(err)
	}
	resp, body := admin.do(http.MethodPatch, path("/api/v1/admin/products/%v", soda), map[string]any{"inventory_mode": "UNTRACKED"})
	expect(t, resp, body, http.StatusConflict, "CONFLICT")

	if _, err := e.pool.Exec(e.ctx, `UPDATE inventory_balances SET on_hand = 0, inventory_value_rial = 0 WHERE product_id = $1`, int64(soda)); err != nil {
		t.Fatal(err)
	}
	resp, body = admin.do(http.MethodPatch, path("/api/v1/admin/products/%v", soda), map[string]any{"inventory_mode": "UNTRACKED", "low_stock_threshold": 2})
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	resp, body = admin.do(http.MethodPatch, path("/api/v1/admin/products/%v", soda), map[string]any{"inventory_mode": "UNTRACKED", "estimated_unit_cost_rial": 100000})
	expect(t, resp, body, http.StatusOK, "")
	if body["low_stock_threshold"] != nil || body["estimated_unit_cost_rial"].(float64) != 100000 {
		t.Errorf("after switch to UNTRACKED: %v", body)
	}
	resp, body = admin.do(http.MethodPatch, path("/api/v1/admin/products/%v", soda), map[string]any{"inventory_mode": "TRACKED"})
	expect(t, resp, body, http.StatusOK, "")
	if body["estimated_unit_cost_rial"] != nil {
		t.Errorf("estimate must be cleared when switching to TRACKED: %v", body)
	}
}

// AC-33 for the catalog: OPERATOR reads and toggles availability, nothing else, and never sees costs.
func TestOperatorCatalogAccess(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	drinks, _, espresso, _, _ := seedCatalog(t, admin)
	e.createUser("barista", "OPERATOR", "barista-pass-1")
	op := e.newClient().login("barista", "barista-pass-1")

	resp, body := op.do(http.MethodGet, "/api/v1/admin/products", nil)
	expect(t, resp, body, http.StatusOK, "")
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "estimated_unit_cost_rial") {
		t.Errorf("OPERATOR must not see cost fields: %s", raw)
	}
	resp, body = op.do(http.MethodGet, "/api/v1/admin/categories", nil)
	expect(t, resp, body, http.StatusOK, "")

	resp, body = op.do(http.MethodPatch, path("/api/v1/admin/products/%v/availability", espresso), map[string]any{"is_available": false})
	expect(t, resp, body, http.StatusOK, "")
	if body["is_available"] != false {
		t.Errorf("availability = %v", body)
	}

	for _, r := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/v1/admin/categories", map[string]any{"name": "x"}},
		{http.MethodPatch, path("/api/v1/admin/categories/%v", drinks), map[string]any{"name": "x"}},
		{http.MethodPost, "/api/v1/admin/products", map[string]any{"name": "x"}},
		{http.MethodPatch, path("/api/v1/admin/products/%v", espresso), map[string]any{"price_rial": 10}},
		{http.MethodPut, path("/api/v1/admin/products/%v/image", espresso), nil},
		{http.MethodDelete, path("/api/v1/admin/products/%v/image", espresso), nil},
	} {
		resp, body := op.do(r.method, r.path, r.body)
		expect(t, resp, body, http.StatusForbidden, "FORBIDDEN")
	}
}

// AC-01 (API part): public menu without auth, active items only, sold-out state, no stock numbers.
func TestPublicMenu(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	drinks, snacks, espresso, soda, cake := seedCatalog(t, admin)
	hidden := admin.mustCreate(t, "/api/v1/admin/products", map[string]any{"category_id": drinks, "name": "پنهان", "price_rial": 10, "inventory_mode": "UNTRACKED"})
	admin.do(http.MethodPatch, path("/api/v1/admin/products/%v", hidden), map[string]any{"is_active": false})
	emptyCat := admin.mustCreate(t, "/api/v1/admin/categories", map[string]any{"name": "خالی"})
	_ = emptyCat
	// cake: tracked with stock; soda: tracked with all stock reserved.
	e.pool.Exec(e.ctx, `UPDATE inventory_balances SET on_hand = 5, inventory_value_rial = 500 WHERE product_id = $1`, int64(cake))
	e.pool.Exec(e.ctx, `UPDATE inventory_balances SET on_hand = 2, reserved = 2, inventory_value_rial = 200 WHERE product_id = $1`, int64(soda))

	anon := e.newClient()
	resp, body := anon.do(http.MethodGet, "/api/v1/public/menu", nil)
	expect(t, resp, body, http.StatusOK, "")
	if resp.Header.Get("Cache-Control") != "no-cache" || resp.Header.Get("ETag") == "" {
		t.Errorf("caching headers: %v", resp.Header)
	}
	ordering := body["ordering"].(map[string]any)
	if ordering["open"] != true || ordering["opens_at"] != "00:00" {
		t.Errorf("ordering = %v", ordering)
	}
	cats := body["categories"].([]any)
	if len(cats) != 2 {
		t.Fatalf("categories = %v (empty categories must be omitted)", cats)
	}
	first := cats[0].(map[string]any)
	if first["id"].(float64) != drinks || first["name"] != "نوشیدنی" {
		t.Errorf("first category = %v", first)
	}
	got := map[float64]map[string]any{}
	for _, c := range cats {
		for _, p := range c.(map[string]any)["products"].([]any) {
			pm := p.(map[string]any)
			got[pm["id"].(float64)] = pm
		}
	}
	if _, ok := got[hidden]; ok {
		t.Error("inactive product listed")
	}
	if got[espresso]["available"] != true || got[espresso]["price_rial"].(float64) != 950000 || got[espresso]["description"] != "دبل شات" {
		t.Errorf("espresso = %v", got[espresso])
	}
	if got[soda]["available"] != false {
		t.Errorf("fully reserved soda must be unavailable: %v", got[soda])
	}
	if got[cake]["available"] != true {
		t.Errorf("cake with stock must be available: %v", got[cake])
	}
	raw, _ := json.Marshal(body)
	for _, leak := range []string{"on_hand", "reserved", "estimated", "inventory_mode", "is_active"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("public menu leaks %q: %s", leak, raw)
		}
	}

	admin.do(http.MethodPatch, path("/api/v1/admin/products/%v/availability", cake), map[string]any{"is_available": false})
	_, body = anon.do(http.MethodGet, "/api/v1/public/menu", nil)
	for _, c := range body["categories"].([]any) {
		if c.(map[string]any)["id"].(float64) == snacks {
			if c.(map[string]any)["products"].([]any)[0].(map[string]any)["available"] != false {
				t.Error("sold-out toggle not reflected")
			}
		}
	}

	etag := ""
	resp, _ = anon.do(http.MethodGet, "/api/v1/public/menu", nil)
	etag = resp.Header.Get("ETag")
	resp, _ = anon.do(http.MethodGet, "/api/v1/public/menu", nil, func(r *http.Request) { r.Header.Set("If-None-Match", etag) })
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match: %d, want 304", resp.StatusCode)
	}
}

func (c *client) mustCreate(t *testing.T, p string, body map[string]any) float64 {
	t.Helper()
	resp, b := c.do(http.MethodPost, p, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST %s: %d %v", p, resp.StatusCode, b)
	}
	return b["id"].(float64)
}

func (c *client) upload(p string, filename string, data []byte) (*http.Response, map[string]any) {
	c.e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", filename)
	fw.Write(data)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPut, c.e.srv.URL+p, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", origin)
	req.Header.Set("X-CSRF-Token", c.csrf)
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp, m
}

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// AC-37: safe image uploads.
func TestProductImageUpload(t *testing.T) {
	e := newEnv(t)
	admin := e.newClient().login("admin", adminPassword)
	_, _, espresso, _, _ := seedCatalog(t, admin)
	imgPath := path("/api/v1/admin/products/%v/image", espresso)

	resp, body := admin.upload(imgPath, "photo.png", testPNG(t, 1600, 1200))
	expect(t, resp, body, http.StatusOK, "")
	url1, _ := body["image_url"].(string)
	if !strings.HasPrefix(url1, "/media/products/") || !strings.HasSuffix(url1, ".jpg") {
		t.Fatalf("image_url = %q", url1)
	}

	media, err := http.Get(e.srv.URL + url1)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(media.Body)
	media.Body.Close()
	if media.StatusCode != 200 || media.Header.Get("Content-Type") != "image/jpeg" || media.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("media: %d %v", media.StatusCode, media.Header)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || cfg.Width != 800 || cfg.Height != 600 {
		t.Errorf("served image: %s %dx%d %v", format, cfg.Width, cfg.Height, err)
	}

	_, menu := e.newClient().do(http.MethodGet, "/api/v1/public/menu", nil)
	raw, _ := json.Marshal(menu)
	if !strings.Contains(string(raw), url1) {
		t.Error("menu does not show the image URL")
	}

	// Replacing removes the previous file.
	resp, body = admin.upload(imgPath, "again.png", testPNG(t, 200, 200))
	expect(t, resp, body, http.StatusOK, "")
	if _, err := os.Stat(filepath.Join(e.images.Dir, filepath.Base(url1))); !os.IsNotExist(err) {
		t.Error("old image file not removed after replacement")
	}
	url2 := body["image_url"].(string)

	resp, body = admin.upload(imgPath, "fake.png", []byte("<svg onload=alert(1)>not an image</svg>"))
	expect(t, resp, body, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE")
	resp, body = admin.upload(imgPath, "huge.png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 3<<20+10)...))
	expect(t, resp, body, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE")
	resp, body = admin.upload(imgPath, "wide.png", testPNG(t, 4100, 2))
	expect(t, resp, body, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	resp, body = admin.upload("/api/v1/admin/products/987654/image", "x.png", testPNG(t, 10, 10))
	expect(t, resp, body, http.StatusNotFound, "NOT_FOUND")

	resp, body = admin.do(http.MethodDelete, imgPath, nil)
	expect(t, resp, body, http.StatusOK, "")
	if body["image_url"] != nil {
		t.Errorf("image_url after delete = %v", body["image_url"])
	}
	if _, err := os.Stat(filepath.Join(e.images.Dir, filepath.Base(url2))); !os.IsNotExist(err) {
		t.Error("image file not removed after delete")
	}
	entries, _ := os.ReadDir(e.images.Dir)
	if len(entries) != 0 {
		t.Errorf("leftover files in upload dir: %v", entries)
	}

	var actions []string
	rows, _ := e.pool.Query(e.ctx, `SELECT action FROM audit_logs WHERE entity_type = 'product' AND action LIKE 'product.image%' ORDER BY id`)
	for rows.Next() {
		var a string
		rows.Scan(&a)
		actions = append(actions, a)
	}
	if strings.Join(actions, ",") != "product.image_set,product.image_set,product.image_remove" {
		t.Errorf("image audit = %v", actions)
	}
	_ = color.Black
}
