package main

import (
	"context"
	"errors"
	"strings"
)

// PairingFailure is optional v1 metadata. Every string is selected here or at
// a fixed call site, never copied from an OS error, response body or stderr.
type PairingFailure struct {
	Stage      string `json:"stage"`
	Code       string `json:"code"`
	HTTPStatus int    `json:"http_status,omitempty"`
	cause      error
}

func (e *PairingFailure) Error() string { return e.Stage + ": " + e.Code }
func (e *PairingFailure) Unwrap() error { return e.cause }

func pairingFailure(stage, code string, err error) error {
	if err == nil {
		return nil
	}
	failure := &PairingFailure{Stage: stage, Code: code, cause: err}
	var api *APIError
	var connection *apiConnectionError
	switch {
	case errors.Is(err, context.Canceled):
		failure.Code = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		failure.Code = "timeout"
	case errors.As(err, &api):
		failure.Code = "api_rejected"
		if api.Status >= 100 && api.Status <= 599 {
			failure.HTTPStatus = api.Status
		}
		switch {
		case api.Status == 429:
			failure.Code = "rate_limited"
		case api.Status == 410 && api.Code == "pair_revoked":
			failure.Code = "pair_revoked"
		case api.Status == 410 && api.Code == "pair_expired":
			failure.Code = "pair_expired"
		}
	case errors.As(err, &connection):
		failure.Code = "api_unavailable"
	}
	return failure
}

func pairingCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "setup" {
		return true
	}
	if args[0] == "status" {
		for _, arg := range args[1:] {
			name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			if strings.HasPrefix(arg, "-") && (name == "installation-json" || name == "pair") {
				return true
			}
		}
	}
	return false
}

func pairingFailureMetadata(err error) *PairingFailure {
	var failure *PairingFailure
	if errors.As(err, &failure) {
		return failure
	}
	return pairingFailure("configuration", "unknown", err).(*PairingFailure)
}
