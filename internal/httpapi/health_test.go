package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/config"
)

// fakePinger lets us test readiness without a real database.
type fakePinger struct{ err error }

func (f fakePinger) Ping(ctx context.Context) error { return f.err }

func handlerWith(p Pinger) http.Handler {
	return NewServer(&config.Config{Port: "0"}, p).Handler
}

func TestHealthEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	handlerWith(fakePinger{nil}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/health = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadyEndpoint(t *testing.T) {
	t.Run("200 when database ping succeeds", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handlerWith(fakePinger{nil}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("/ready = %d, want %d", rec.Code, http.StatusOK)
		}
	})

	t.Run("503 when database ping fails", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handlerWith(fakePinger{errors.New("connection refused")}).
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("/ready = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
	})
}

func TestMethodRouting(t *testing.T) {
	// POST /health should not match the GET-only route.
	rec := httptest.NewRecorder()
	handlerWith(fakePinger{nil}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/health", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /health = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
