package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
)

// TenantResolver resolves a raw API key to a tenant id. Implemented by the
// service layer; the raw key is never logged here or there.
type TenantResolver interface {
	ResolveTenant(ctx context.Context, rawAPIKey string) (tenantID string, ok bool, err error)
}

type ctxKey string

const tenantIDKey ctxKey = "tenantID"

// authMiddleware requires a valid Bearer API key and puts the tenant id in the
// request context. Missing/invalid/revoked keys get 401.
func authMiddleware(resolver TenantResolver, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := bearerToken(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized", "missing or malformed Authorization: Bearer header")
				return
			}
			tenantID, found, err := resolver.ResolveTenant(r.Context(), raw)
			if err != nil {
				logger.Error("auth: resolve tenant failed", "err", err)
				writeError(w, http.StatusInternalServerError, "internal", "authentication error")
				return
			}
			if !found {
				writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or revoked API key")
				return
			}
			ctx := context.WithValue(r.Context(), tenantIDKey, tenantID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) <= len(p) || !strings.EqualFold(h[:len(p)], p) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(p):])
	if tok == "" {
		return "", false
	}
	return tok, true
}

func tenantIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(tenantIDKey).(string)
	return v
}
