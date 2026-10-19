package catalog

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // decoder registration
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decoder registration
)

// Upload limits and output format from docs/SECURITY.md §7.
const (
	MaxImageBytes  = 3 << 20
	MaxImageSide   = 4000
	OutputMaxSide  = 800
	OutputQuality  = 82
	mediaURLPrefix = "/media/products/"
)

var (
	ErrUnsupportedImage = errors.New("image must be JPEG, PNG or WebP")
	ErrImageDimensions  = errors.New("image dimensions are out of range")
	ErrImageTooLarge    = errors.New("image file is too large")
)

var imageName = regexp.MustCompile(`^[0-9a-f]{32}\.jpg$`)

// ImageURL is the public URL of a stored product image.
func ImageURL(name string) string { return mediaURLPrefix + name }

// NormalizeImage validates an upload by content (never by extension or client MIME type), decodes
// it, scales it down to OutputMaxSide and re-encodes it as JPEG. Re-encoding drops metadata and
// anything that is not pixel data (polyglot payloads).
func NormalizeImage(data []byte) ([]byte, error) {
	if len(data) > MaxImageBytes {
		return nil, ErrImageTooLarge
	}
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png", "image/webp":
	default:
		return nil, ErrUnsupportedImage
	}
	// Check dimensions from the header before allocating the full image (decompression bombs).
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, ErrUnsupportedImage
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxImageSide || cfg.Height > MaxImageSide {
		return nil, ErrImageDimensions
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrUnsupportedImage
	}

	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if w > OutputMaxSide || h > OutputMaxSide {
		if w >= h {
			h = max(1, h*OutputMaxSide/w)
			w = OutputMaxSide
		} else {
			w = max(1, w*OutputMaxSide/h)
			h = OutputMaxSide
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	// JPEG has no alpha: composite transparent PNG/WebP onto white instead of black.
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: OutputQuality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return out.Bytes(), nil
}

// ImageStore keeps product images on local disk, outside any web root (docs/ARCHITECTURE.md §4).
type ImageStore struct {
	Dir string // <UPLOAD_DIR>/products
}

// NewImageStore creates the directory if needed.
func NewImageStore(uploadDir string) (*ImageStore, error) {
	dir := filepath.Join(uploadDir, "products")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create image dir: %w", err)
	}
	return &ImageStore{Dir: dir}, nil
}

// Save writes data under a random name atomically and returns the name.
func (s *ImageStore) Save(data []byte) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	name := hex.EncodeToString(b[:]) + ".jpg"
	tmp, err := os.CreateTemp(s.Dir, ".upload-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.Dir, name)); err != nil {
		return "", err
	}
	return name, nil
}

// Remove deletes a stored image; unknown or malformed names are ignored.
func (s *ImageStore) Remove(name string) {
	if imageName.MatchString(name) {
		_ = os.Remove(filepath.Join(s.Dir, name))
	}
}

// MediaHandler serves /media/products/{name}. Only names produced by Save are accepted, the
// content type is fixed, and files are immutable so they can be cached for a year.
func (s *ImageStore) MediaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := filepath.Base(r.URL.Path)
		if r.URL.Path != mediaURLPrefix+name || !imageName.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(filepath.Join(s.Dir, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "image/jpeg")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeContent(w, r, "", time.Time{}, f)
	})
}
