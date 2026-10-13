package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type API struct{ Client *http.Client }
type APIError struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string { return fmt.Sprintf("API HTTP %d (%s)", e.Status, e.Code) }
func newAPI() API {
	return API{&http.Client{Timeout: 4 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("redirect refused") }}}
}
func (a API) request(ctx context.Context, p Pair, method, path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		if len(b) > 8192 {
			return nil, errors.New("request exceeds limit")
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.APIURL, "/")+path, reader)
	if err != nil {
		return nil, errors.New("invalid API request")
	}
	req.Header.Set("Authorization", "Bearer "+p.SendToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, errors.New("API unavailable")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	if err != nil || len(b) > 16384 {
		return nil, errors.New("invalid API response")
	}
	v := map[string]any{}
	if json.Unmarshal(b, &v) != nil {
		return nil, errors.New("invalid API response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code, _ := v["error"].(string)
		if len(code) > 80 || strings.ContainsAny(code, "\r\n") {
			code = "request_failed"
		}
		retryAfter := time.Duration(0)
		if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds > 0 {
			retryAfter = time.Duration(seconds) * time.Second
		}
		return v, &APIError{Status: resp.StatusCode, Code: code, RetryAfter: retryAfter}
	}
	return v, nil
}
func (a API) send(ctx context.Context, p Pair, e Envelope) (map[string]any, error) {
	var v map[string]any
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		v, err = a.request(ctx, p, "POST", "/v1/pairs/"+p.PairID+"/events", e)
		if err == nil {
			return v, nil
		}
		var ae *APIError
		if errors.As(err, &ae) && ae.Status != 429 && ae.Status < 500 {
			return v, err
		}
		if ctx.Err() != nil {
			return v, err
		}
		if attempt == 0 {
			delay := 100 * time.Millisecond
			if ae != nil && ae.RetryAfter > delay {
				delay = ae.RetryAfter
			}
			if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < delay+time.Second {
				return v, err
			}
			select {
			case <-ctx.Done():
				return v, err
			case <-time.After(delay):
			}
		}
	}
	return v, err
}
func setup(ctx context.Context, s Store, a API, in io.Reader) (map[string]any, error) {
	var p SetupPacket
	if err := readFrameContext(ctx, in, &p); err != nil {
		return nil, err
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	id, err := s.installation()
	if err != nil {
		return nil, err
	}
	if p.InstallationID != id {
		return nil, errors.New("installation identity changed")
	}
	pair := Pair{p, "pending"}
	if err = s.savePair(ctx, pair); err != nil {
		return nil, err
	}
	v, err := a.request(ctx, pair, "POST", "/v1/pairs/"+p.PairID+"/claim", map[string]string{"installation_id": id, "claim_id": p.PairID})
	if err != nil {
		return nil, err
	}
	if v["status"] != "active" {
		return nil, errors.New("API has not activated pair")
	}
	pair.Status = "active"
	if err = s.savePair(ctx, pair); err != nil {
		return nil, err
	}
	// Read the durable local value before acknowledging the SSH machine call.
	saved, err := s.pairs()
	if err != nil || saved[p.PairID] != pair {
		return nil, errors.New("local pair readback failed")
	}
	return map[string]any{"v": 1, "pair_id": p.PairID, "installation_id": id, "status": "active", "local_saved": true}, nil
}
