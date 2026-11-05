package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Nezzy-joe/flyrank-capstone-metering-billing/internal/service"
)

// Meterer is the metering dependency for the generate handler.
type Meterer interface {
	Generate(ctx context.Context, tenantID, idempotencyKey string, in service.GenerateInput) (service.Result, error)
}

const (
	maxGenerateBody      = 1 << 20 // 1 MiB
	maxIdempotencyKeyLen = 255
	maxTokensPerField    = 10_000_000
)

type generateRequest struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	ReasoningTokens   int64 `json:"reasoning_tokens"`
}

func (req generateRequest) validate() (msg string, ok bool) {
	fields := []struct {
		name string
		v    int64
	}{
		{"input_tokens", req.InputTokens},
		{"cached_input_tokens", req.CachedInputTokens},
		{"output_tokens", req.OutputTokens},
		{"reasoning_tokens", req.ReasoningTokens},
	}
	for _, f := range fields {
		if f.v < 0 {
			return f.name + " must be >= 0", false
		}
		if f.v > maxTokensPerField {
			return f.name + " exceeds the maximum allowed", false
		}
	}
	return "", true
}

// handleGenerate validates the request at the boundary, then delegates the
// metering transaction to the service. No SQL here.
func handleGenerate(meter Meterer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := tenantIDFrom(r.Context())

		idemKey := r.Header.Get("Idempotency-Key")
		if idemKey == "" || len(idemKey) > maxIdempotencyKeyLen {
			writeError(w, http.StatusBadRequest, "invalid_idempotency_key",
				"Idempotency-Key header is required and must be 1..255 characters")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxGenerateBody)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var req generateRequest
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON with known fields only")
			return
		}
		if msg, ok := req.validate(); !ok {
			writeError(w, http.StatusBadRequest, "invalid_request", msg)
			return
		}

		res, err := meter.Generate(r.Context(), tenantID, idemKey, service.GenerateInput(req))
		if err != nil {
			logger.Error("generate failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "failed to process request")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.Status)
		_, _ = w.Write(res.Body)
	}
}
