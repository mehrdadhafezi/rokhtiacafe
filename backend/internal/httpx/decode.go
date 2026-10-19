package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
)

// MaxJSONBody is the request body limit from docs/API-CONTRACT.md §2.
const MaxJSONBody = 64 << 10

// DecodeJSON strictly decodes one JSON object into dst: unknown fields are rejected, and an
// explicit null is accepted only for fields whose type declares AllowsNull (opt.Field). A plain
// pointer field therefore means "may be omitted", never "may be null", so `{"price_rial": null}`
// is a validation error rather than a silent no-op. On failure it writes the error response and
// returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/json") {
		Error(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "expected application/json")
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxJSONBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			Error(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body too large")
		} else {
			Error(w, http.StatusBadRequest, "BAD_REQUEST", "could not read body")
		}
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if strings.HasPrefix(err.Error(), "json: unknown field") {
			ValidationError(w, map[string]string{strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`): "unknown"})
		} else {
			Error(w, http.StatusBadRequest, "BAD_REQUEST", "malformed JSON body")
		}
		return false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		Error(w, http.StatusBadRequest, "BAD_REQUEST", "body must contain a single JSON object")
		return false
	}
	if bad := nullsNotAllowed(body, dst); len(bad) > 0 {
		ValidationError(w, bad)
		return false
	}
	return true
}

type nullable interface{ AllowsNull() bool }

var nullableType = reflect.TypeOf((*nullable)(nil)).Elem()

// nullsNotAllowed returns the top-level keys that are null in body but whose destination field
// does not accept null.
func nullsNotAllowed(body []byte, dst any) map[string]string {
	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return nil
	}
	t := reflect.TypeOf(dst)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	allowed := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" {
			name = f.Name
		}
		allowed[strings.ToLower(name)] = f.Type.Implements(nullableType)
	}
	bad := map[string]string{}
	for k, v := range raw {
		if string(bytes.TrimSpace(v)) == "null" && !allowed[strings.ToLower(k)] {
			bad[k] = "must not be null"
		}
	}
	return bad
}

// ValidationError writes 422 VALIDATION_ERROR with per-field reasons.
func ValidationError(w http.ResponseWriter, fields map[string]string) {
	JSON(w, http.StatusUnprocessableEntity, ErrorBody{Error: ErrorDetail{
		Code: "VALIDATION_ERROR", Message: "validation failed", Details: map[string]any{"fields": fields},
	}})
}
