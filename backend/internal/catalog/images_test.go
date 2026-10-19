package catalog

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func pngBytes(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestNormalizeImageResizesAndReencodes(t *testing.T) {
	out, err := NormalizeImage(pngBytes(t, 1600, 800, color.NRGBA{R: 200, A: 255}))
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(bytes.NewReader(out))
	if err != nil || format != "jpeg" {
		t.Fatalf("output format %q err %v", format, err)
	}
	if b := img.Bounds(); b.Dx() != 800 || b.Dy() != 400 {
		t.Errorf("resized to %v, want 800x400", b.Size())
	}

	small, err := NormalizeImage(pngBytes(t, 300, 500, color.NRGBA{G: 200, A: 255}))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := jpeg.DecodeConfig(bytes.NewReader(small))
	if cfg.Width != 300 || cfg.Height != 500 {
		t.Errorf("small image must not be upscaled: %dx%d", cfg.Width, cfg.Height)
	}
}

func TestNormalizeImageFlattensTransparencyOntoWhite(t *testing.T) {
	out, err := NormalizeImage(pngBytes(t, 10, 10, color.NRGBA{}))
	if err != nil {
		t.Fatal(err)
	}
	img, _ := jpeg.Decode(bytes.NewReader(out))
	r, g, b, _ := img.At(5, 5).RGBA()
	if r < 0xf000 || g < 0xf000 || b < 0xf000 {
		t.Errorf("transparent pixel became %v, want white", img.At(5, 5))
	}
}

func TestNormalizeImageRejects(t *testing.T) {
	cases := map[string]struct {
		data []byte
		want error
	}{
		"text":           {[]byte("hello, I am definitely a picture"), ErrUnsupportedImage},
		"html":           {[]byte("<html><script>alert(1)</script></html>"), ErrUnsupportedImage},
		"truncated png":  {pngBytes(t, 20, 20, color.Black)[:40], ErrUnsupportedImage},
		"too wide":       {pngBytes(t, 4001, 1, color.Black), ErrImageDimensions},
		"too many bytes": {append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxImageBytes)...), ErrImageTooLarge},
	}
	for name, c := range cases {
		if _, err := NormalizeImage(c.data); err != c.want {
			t.Errorf("%s: got %v, want %v", name, err, c.want)
		}
	}
}

func TestImageStoreAndMediaHandler(t *testing.T) {
	s, err := NewImageStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	name, err := s.Save([]byte("\xff\xd8\xff fake jpeg"))
	if err != nil || !imageName.MatchString(name) {
		t.Fatalf("Save = %q, %v", name, err)
	}
	h := s.MediaHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ImageURL(name), nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("serve: %d %v", rec.Code, rec.Header())
	}

	for _, p := range []string{"/media/products/../../etc/passwd", "/media/products/" + name + "/x", "/media/products/evil.svg",
		"/media/products/ABCDEF0123456789ABCDEF0123456789.jpg", "/media/other/" + name} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, ImageURL(name), nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}

	s.Remove(name)
	if _, err := os.Stat(filepath.Join(s.Dir, name)); !os.IsNotExist(err) {
		t.Error("file not removed")
	}
	s.Remove("../../important") // ignored, must not panic or delete anything
}
