package api

import (
	"encoding/json"
	"net/http"
)

const (
	CodeMissingInput        = "missing_input"
	CodeInputTooLong        = "input_too_long"
	CodeAmbiguousTLDFilter  = "ambiguous_tld_filter"
	CodeUnknownTLD          = "unknown_tld"
	CodeBothTiersFailed     = "both_tiers_failed"
	CodeUnknownCategory     = "unknown_category"
	CodeTooManyUnavailable  = "too_many_unavailable_domains"
	CodeTooManyInspireFrom  = "too_many_inspire_from"
)

type errorResponse struct {
	Error   string   `json:"error"`
	Code    string   `json:"code"`
	Details []string `json:"details,omitempty"`
}

func WriteError(w http.ResponseWriter, status int, code, msg string, details ...string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := errorResponse{Error: msg, Code: code}
	if len(details) > 0 {
		resp.Details = details
	}
	json.NewEncoder(w).Encode(resp)
}
