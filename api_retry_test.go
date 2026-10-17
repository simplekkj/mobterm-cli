package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSendDailyRetryAfterExitsWithinCommandBudget(t *testing.T) {
	var calls atomic.Int32
	e := Envelope{V: 1, EventID: "event_fixture00000000000", Nonce: "unchanged-nonce", Ciphertext: "unchanged-ciphertext"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var got Envelope
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got != e {
			t.Error("request changed envelope", err)
		}
		w.Header().Set("Retry-After", "86400")
		w.WriteHeader(http.StatusTooManyRequests)
		printJSON(w, map[string]string{"error": "notification_global_daily_limit"})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	_, err := (API{server.Client()}).send(ctx, testPair("install_fixture000000000000", server.URL), e)
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.RetryAfter != 24*time.Hour || apiError.Code != "notification_global_daily_limit" {
		t.Fatal("daily response lost", err)
	}
	if calls.Load() != 1 {
		t.Fatal("daily limit retried", calls.Load())
	}
	if time.Since(started) > time.Second {
		t.Fatal("waited despite next-day retry")
	}
}
