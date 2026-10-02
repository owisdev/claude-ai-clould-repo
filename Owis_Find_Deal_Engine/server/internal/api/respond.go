package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxBodyBytes = 64 << 10

// Error codes returned to clients.
const (
	CodeInvalidRequest     = "invalid_request"
	CodeUnsupportedCountry = "unsupported_country"
	CodeUnauthorized       = "unauthorized"
	CodeRateLimited        = "rate_limited"
	CodeQuotaExceeded      = "quota_exceeded"
	CodePaymentRequired    = "payment_required"
	CodeUnavailable        = "service_unavailable"
	CodeUpstream           = "upstream_error"
	CodeInternal           = "internal_error"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: msg}})
}

// decodeJSON strictly decodes a single JSON object from a size-limited body.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		return errors.New("Content-Type must be application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return fmt.Errorf("body must not exceed %d bytes", maxBodyBytes)
		case errors.Is(err, io.EOF):
			return errors.New("body must not be empty")
		default:
			return fmt.Errorf("invalid JSON: %v", err)
		}
	}
	if dec.More() {
		return errors.New("body must contain a single JSON object")
	}
	return nil
}
