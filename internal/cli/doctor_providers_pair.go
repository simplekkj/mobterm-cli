package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mobterm/mobterm-cli/internal/doctor"
)

type doctorCatalog struct {
	dirErr, identityErr, pairsErr error
	installation                  string
	pairs                         map[string]Pair
	retired                       map[string]string
}

func doctorReadCatalog(s Store) doctorCatalog {
	c := doctorCatalog{dirErr: doctorDirectory(s.Dir), pairs: map[string]Pair{}, retired: map[string]string{}}
	if c.dirErr != nil {
		c.identityErr = c.dirErr
		return c
	}
	var id storedInstallation
	c.identityErr = doctorReadJSON(filepath.Join(s.Dir, "installation.json"), 4096, &id)
	if c.identityErr == nil {
		if id.V != 1 || !opaqueID.MatchString(id.ID) {
			c.identityErr = errDoctorUnsafe
		} else {
			c.installation = id.ID
		}
	}
	c.pairsErr = doctorReadJSON(filepath.Join(s.Dir, "pairs.json"), 2<<20, &c.pairs)
	if errors.Is(c.pairsErr, os.ErrNotExist) {
		c.pairsErr = nil
	}
	if c.pairs == nil {
		c.pairsErr = errDoctorUnsafe
	}
	var history []retiredPair
	e := doctorReadJSON(filepath.Join(s.Dir, "pair-history.json"), 2<<20, &history)
	if e != nil && !errors.Is(e, os.ErrNotExist) || len(history) > maxRetiredPairs {
		c.pairsErr = errDoctorUnsafe
	}
	for _, retired := range history {
		if !bodyHashPattern.MatchString(retired.PairRef) {
			c.pairsErr = errDoctorUnsafe
			continue
		}
		c.retired[retired.PairRef] = retired.Status
	}
	return c
}

func (c doctorCatalog) pairReason(id string, p Pair) string {
	switch {
	case c.pairsErr != nil || c.dirErr != nil:
		return "pair_store_invalid"
	case id != p.PairID || p.SetupPacket.validate() != nil:
		return "pair_record_invalid"
	case c.installation == "" || c.installation != p.InstallationID:
		return "installation_mismatch"
	}
	if _, retired := c.retired[digest(id)]; retired {
		return "pair_retirement_pending"
	}
	switch p.Status {
	case "active", "pending", "pending_review", "review_required":
		return ""
	default:
		return "pair_record_invalid"
	}
}

func doctorPairScope(id string) string { return "pair." + digest(id)[:24] }
func doctorRequestedScope(o doctorOptions) string {
	if o.Pair != "" {
		return doctorPairScope(o.Pair)
	}
	return doctorHost
}

func doctorPairChecks(s Store, a API, c doctorCatalog, o doctorOptions) []doctor.Check {
	store := doctor.Key("storage.pairs", doctorHost)
	var ids []string
	if o.Pair != "" {
		if _, ok := c.pairs[o.Pair]; ok {
			ids = []string{o.Pair}
		}
	} else {
		ids = slices.Sorted(maps.Keys(c.pairs))
	}
	if len(ids) == 0 || c.pairsErr != nil {
		scope := doctorRequestedScope(o)
		reason, status, feature := "pair_not_configured", "skipped", "not_configured"
		if o.Pair != "" {
			status, reason = "warn", "selected_pair_missing"
			if _, ok := c.retired[digest(o.Pair)]; ok {
				reason = "selected_pair_retired"
			}
		}
		checks := []doctor.Check{}
		for _, item := range []struct{ id, feature string }{
			{"network.service", "api.reachability"}, {"pair.local", "pairing"}, {"interaction.policy", "interaction"},
		} {
			checks = append(checks, doctor.Check{ID: item.id, Scope: scope, Depends: []string{store}, Affects: []doctor.Feature{doctorFeature(item.feature, scope, "unknown")}, Run: func(context.Context) doctor.Result {
				r := doctorFact(status, reason, "configuration")
				r.Features = []doctor.Feature{doctorFeature(item.feature, scope, feature)}
				return r
			}})
		}
		return checks
	}
	var checks []doctor.Check
	inventory := []string{store}
	if len(ids) > 16 {
		n := len(ids) - 16
		checks = append(checks, doctor.Check{ID: "pair.limit", Scope: doctorHost, Run: func(context.Context) doctor.Result {
			r := doctorFact("warn", "select_pair_for_remaining", "configuration")
			r.Partial = true
			r.Counts = map[string]int{"remaining_pairs": n}
			return r
		}})
		// Every consumer of the expanded inventory includes this row even
		// when a scoped run selects only an origin or a deep policy check.
		inventory = append(inventory, doctor.Key("pair.limit", doctorHost))
		ids = ids[:16]
	}
	origins := map[string]bool{}
	for _, id := range ids {
		p := c.pairs[id]
		scope := doctorPairScope(id)
		local := doctor.Key("pair.local", scope)
		checks = append(checks, doctor.Check{ID: "pair.local", Scope: scope, Depends: append(slices.Clone(inventory), doctor.Key("storage.identity", doctorHost)), Run: func(context.Context) doctor.Result {
			if reason := c.pairReason(id, p); reason != "" {
				return doctorFact("fail", reason, "configuration")
			}
			r := doctorFact("pass", "pair_record_valid", "configuration")
			switch p.Status {
			case "pending", "pending_review":
				r.Status, r.Reason = "warn", "local_pair_pending"
			case "review_required":
				r.Status, r.Reason = "warn", "local_pair_requires_review"
			}
			r.Details = map[string]string{"local_state": p.Status, "pair_id": id}
			return r
		}})
		deps := []string{local}
		if c.pairReason(id, p) == "" {
			origin := strings.TrimSuffix(p.APIURL, "/")
			originScope := "origin." + digest(origin)[:24]
			if !origins[origin] {
				origins[origin] = true
				checks = append(checks, doctor.Check{ID: "network.service", Scope: originScope, Depends: inventory, Run: func(ctx context.Context) doctor.Result {
					return doctorService(ctx, a, origin, o.Offline)
				}})
			}
			deps = append(deps, doctor.Key("network.service", originScope))
		} else {
			// A network-only run must still explain why this configured origin
			// could not be trusted rather than producing an empty pass.
			checks = append(checks, doctor.Check{ID: "network.service", Scope: scope, Depends: []string{local}, Run: func(context.Context) doctor.Result {
				return doctorFact("unknown", "origin_unconfirmed", "configuration")
			}})
		}
		var remoteReady bool // Read only after pair.remote has returned successfully.
		checks = append(checks, doctor.Check{ID: "pair.remote", Scope: scope, Depends: deps, Affects: doctorPairFeatures(scope, "unknown"), Run: func(ctx context.Context) doctor.Result {
			r := doctorRemotePair(ctx, s, a, p, o.Offline)
			remoteReady = r.Status == "pass" && p.Status == "active"
			return r
		}}, doctor.Check{ID: "interaction.policy", Scope: scope, Depends: []string{local}, Run: func(ctx context.Context) doctor.Result {
			return doctorInteractions(ctx, s, p, o)
		}})
		if o.Deep {
			dep := []string{doctor.Key("pair.remote", scope)}
			for _, id := range []string{"interaction.service", "delivery.policy"} {
				checks = append(checks, doctor.Check{ID: id, Scope: scope, Depends: dep, Run: func(ctx context.Context) doctor.Result {
					if o.Offline {
						return doctorFact("skipped", "offline", "configuration")
					}
					if !remoteReady {
						return doctorFact("skipped", "active_pair_required", "service")
					}
					return doctorCapabilities(ctx, s, a, p, id, o.Agent)
				}})
			}
		}
	}
	return checks
}

func doctorPairFeatures(scope, status string) []doctor.Feature {
	return []doctor.Feature{doctorFeature("pairing", scope, status), doctorFeature("notifications", scope, status), doctorFeature("job.notification", scope, status), doctorFeature("interaction.authorization", scope, status)}
}

func doctorService(ctx context.Context, a API, origin string, offline bool) doctor.Result {
	if offline {
		return doctorFact("skipped", "offline", "configuration")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/health", nil)
	if err != nil {
		return doctorFact("fail", "origin_invalid", "configuration")
	}
	// No pair credentials are needed for /health. Refuse every redirect even
	// when a fixture provided its own transport.
	client := *a.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return doctorNetworkFailure(err)
	}
	defer resp.Body.Close()
	var health struct {
		Service  string
		Protocol int
	}
	r := doctorFact("pass", "service_responded", "service")
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	switch {
	case resp.StatusCode != 200:
		r.Status, r.Reason = "fail", "health_http_failure"
	case err != nil || len(b) > 4096 || json.Unmarshal(b, &health) != nil || health.Service != "mobterm-api" || health.Protocol != 1:
		r.Status, r.Reason = "fail", "health_receipt_invalid"
	}
	r.Counts = map[string]int{"http_status": resp.StatusCode}
	r.Details = map[string]string{}
	if doctorSafeDetail(origin) {
		r.Details["origin"] = origin
	}
	if id := resp.Header.Get("X-Request-ID"); diagnosticRequestID.MatchString(id) {
		r.Details["request_id"] = id
	}
	if date, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
		skew := int(time.Since(date).Seconds())
		if skew >= -86400*365 && skew <= 86400*365 {
			if skew < 0 {
				skew = -skew
			}
			r.Counts["clock_gap_seconds"] = skew
			if skew > 300 && r.Status == "pass" {
				r.Status, r.Reason = "warn", "service_clock_differs"
			}
		}
	}
	return r
}

func doctorNetworkFailure(err error) doctor.Result {
	var dns *net.DNSError
	var cert *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var op *net.OpError
	var remote *APIError
	reason, status := "connection_failed", "fail"
	switch {
	case errors.Is(err, context.Canceled):
		reason, status = "cancelled", "unknown"
	case errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err):
		reason, status = "request_timed_out", "unknown"
	case errors.As(err, &dns):
		reason = "dns_failed"
	case errors.As(err, &cert) || errors.As(err, &authority):
		reason = "tls_failed"
	case errors.As(err, &op) && op.Op == "proxyconnect":
		reason = "proxy_failed"
	case errors.As(err, &op) && op.Op == "dial":
		reason = "tcp_failed"
	case errors.As(err, &remote):
		reason = "request_http_failure"
	}
	return doctorFact(status, reason, "network")
}

func doctorRemotePair(ctx context.Context, s Store, a API, p Pair, offline bool) doctor.Result {
	scope := doctorPairScope(p.PairID)
	r := doctorFact("skipped", "offline", "configuration")
	r.Features = doctorPairFeatures(scope, "unknown")
	if offline {
		return r
	}
	m, err := doctorPairGET(ctx, s, a, p, "/v1/pairs/"+url.PathEscape(p.PairID))
	if errors.Is(err, errDoctorPairChanged) {
		r.Status, r.Reason = "unknown", "pair_changed"
		return r
	}
	if err != nil {
		r = doctorNetworkFailure(err)
		r.Features = doctorPairFeatures(scope, "unknown")
		var remote *APIError
		if errors.As(err, &remote) {
			r.Counts = map[string]int{"http_status": remote.Status}
			if remote.Status == 410 && (remote.Code == "pair_revoked" || remote.Code == "pair_expired") {
				r.Reason, r.Evidence, r.Features = remote.Code, "service", doctorPairFeatures(scope, "unavailable")
			} else if remote.Status == 401 || remote.Status == 403 || remote.Status == 404 {
				r.Status, r.Reason, r.Evidence = "warn", "pair_auth_unconfirmed", "service"
			}
		}
		return r
	}
	r.Status, r.Reason, r.Evidence = "unknown", "pair_receipt_invalid", "service"
	if m["pair_id"] != p.PairID {
		return r
	}
	inst, ok := m["installation_id"].(string)
	if !ok || !opaqueID.MatchString(inst) {
		return r
	}
	if inst != p.InstallationID {
		r.Status, r.Reason, r.Features = "fail", "installation_mismatch", doctorPairFeatures(scope, "unavailable")
		return r
	}
	state, _ := m["status"].(string)
	switch state {
	case "revoked", "expired":
		r.Status, r.Reason, r.Features = "fail", "pair_"+state, doctorPairFeatures(scope, "unavailable")
	case "pending":
		r.Status, r.Reason, r.Features = "warn", "pair_pending", doctorPairFeatures(scope, "unavailable")
	case "active":
		r.Status, r.Reason = "pass", "pair_active"
		r.Features[0].Status = "ready" // Only the authenticated pairing was verified.
		if enabled, ok := m["notifications_enabled"].(bool); ok && !enabled {
			r.Features[1].Status, r.Features[2].Status = "disabled", "disabled"
			r.Reason = "pair_active_notifications_disabled"
		}
		if p.Status != "active" {
			r.Status, r.Reason, r.Features[0].Status = "warn", "pair_reconcile_needed", "degraded"
		}
	}
	return r
}

var errDoctorPairChanged = errors.New("diagnostic pair changed")

// A retired or replaced pair must never lend its authority to another request.
// Deliberately use the underlying GET; pairQuery also retires local records.
func doctorPairGET(ctx context.Context, s Store, a API, p Pair, path string) (map[string]any, error) {
	stillCurrent := func() bool {
		current := doctorReadCatalog(s)
		return current.pairs[p.PairID] == p && current.pairReason(p.PairID, p) == ""
	}
	if !stillCurrent() {
		return nil, errDoctorPairChanged
	}
	client := *a.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	m, err := (API{Client: &client}).request(ctx, p, http.MethodGet, path, nil)
	if !stillCurrent() {
		return nil, errDoctorPairChanged
	}
	return m, err
}

func doctorCapabilities(ctx context.Context, s Store, a API, p Pair, check, selectedAgent string) doctor.Result {
	suffix := "capabilities"
	if check == "interaction.service" {
		suffix = "interaction-capabilities"
	}
	m, err := doctorPairGET(ctx, s, a, p, "/v2/pairs/"+url.PathEscape(p.PairID)+"/"+suffix)
	if errors.Is(err, errDoctorPairChanged) {
		return doctorFact("unknown", "pair_changed", "configuration")
	}
	if err != nil {
		var remote *APIError
		if errors.As(err, &remote) && remote.Status == 404 {
			return doctorFact("skipped", "capabilities_not_supported", "service")
		}
		return doctorNetworkFailure(err)
	}
	scope := doctorPairScope(p.PairID)
	if check == "interaction.service" {
		c, err := parseInteractionCapabilityEvidence(m, p)
		if err != nil {
			return doctorFact("unknown", "capability_receipt_invalid", "service")
		}
		r := doctorFact("pass", "interaction_grants_read", "service")
		r.Counts = map[string]int{"wire": 1}
		r.Details = map[string]string{}
		for _, agent := range []string{"codex", "claude", "opencode", "pi"} {
			if selectedAgent != "" && agent != selectedAgent {
				continue
			}
			var kinds []string
			for _, kind := range []string{"approval", "question", "form", "elicitation", "planApproval", "followupMessage"} {
				if c.Enabled && c.Interactions[agent][kind] {
					kinds = append(kinds, kind)
				}
			}
			if len(kinds) > 0 {
				r.Details[agent] = strings.Join(kinds, ",")
			}
		}
		if len(r.Details) == 0 {
			r.Reason = "remote_grants_disabled"
		}
		return r
	}
	c, err := parseRunCapabilities(m, p)
	if err != nil {
		return doctorFact("unknown", "capability_receipt_invalid", "service")
	}
	r := doctorFact("pass", "capability_policy_read", "service")
	for _, field := range []struct {
		id      string
		enabled bool
	}{
		{"activity.policy", c.Enabled && c.States}, {"inbox.policy", c.Enabled && c.Inbox},
	} {
		state := "disabled"
		if field.enabled {
			state = "ready"
		}
		r.Features = append(r.Features, doctorFeature(field.id, scope, state))
	}
	// Older API versions omit quick-command-specific facts. Absence leaves
	// that policy unknown. A policy permits reporting only, not execution.
	state := "unknown"
	enabled, hasNotifications := m["notifications_enabled"].(bool)
	supported, hasSupported := m["quick_command_v3"].(bool)
	completed, hasCompleted := m["quick_command_completed"].(bool)
	if hasNotifications && hasSupported && hasCompleted {
		state = "disabled"
		if c.Enabled && c.Inbox && enabled && supported && completed {
			var policy quickCommandPolicy
			b, _ := json.Marshal(m)
			if json.Unmarshal(b, &policy) != nil || policy.CapabilityGeneration < 1 || policy.PolicyGeneration < 1 {
				return doctorFact("unknown", "capability_receipt_invalid", "service")
			}
			state = "ready"
		}
	}
	r.Features = append(r.Features, doctorFeature("job.notification_policy", scope, state))
	return r
}
