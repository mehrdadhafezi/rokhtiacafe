package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A request the client abandoned is not logged as a server error (C6 browser runs: navigating
// away cancelled a dashboard query and produced a false ERROR line); real failures still are.
func TestInternalErrorLogging(t *testing.T) {
	var buf bytes.Buffer
	a := &API{d: Deps{Logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/dashboard", nil).WithContext(ctx)
	a.internal(httptest.NewRecorder(), r, fmt.Errorf("query: %w", context.Canceled))
	if strings.Contains(buf.String(), "level=ERROR") || !strings.Contains(buf.String(), "cancelled by the client") {
		t.Errorf("client cancellation logged as: %s", buf.String())
	}

	buf.Reset()
	w := httptest.NewRecorder()
	a.internal(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/dashboard", nil), errors.New("boom"))
	if !strings.Contains(buf.String(), "level=ERROR") || w.Code != http.StatusInternalServerError {
		t.Errorf("real failure: %d %s", w.Code, buf.String())
	}

	// A cancellation the client did not cause (e.g. a server-side timeout) stays an error.
	buf.Reset()
	a.internal(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil), context.Canceled)
	if !strings.Contains(buf.String(), "level=ERROR") {
		t.Errorf("server-side cancellation hidden: %s", buf.String())
	}
}
