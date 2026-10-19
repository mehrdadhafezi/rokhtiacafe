package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// MaxJSONBody is the request body limit from docs/API-CONTRACT.md §2.
const MaxJSONBody = 64 << 10

// DecodeJSON strictly decodes one JSON object into dst. On failure it writes the error
// response and returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/json") {
		Error(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "expected application/json")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			Error(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body too large")
		case strings.HasPrefix(err.Error(), "json: unknown field"):
			ValidationError(w, map[string]string{strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`): "unknown"})
		default:
			Error(w, http.StatusBadRequest, "BAD_REQUEST", "malformed JSON body")
		}
		return false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		Error(w, http.StatusBadRequest, "BAD_REQUEST", "body must contain a single JSON object")
		return false
	}
	return true
}

// ValidationError writes 422 VALIDATION_ERROR with per-field reasons.
func ValidationError(w http.ResponseWriter, fields map[string]string) {
	JSON(w, http.StatusUnprocessableEntity, ErrorBody{Error: ErrorDetail{
		Code: "VALIDATION_ERROR", Message: "validation failed", Details: map[string]any{"fields": fields},
	}})
}
