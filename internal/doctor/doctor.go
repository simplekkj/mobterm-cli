// Package doctor runs read-only, scoped checks and preserves partial evidence.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Feature struct {
	ID     string `json:"id"`
	Scope  string `json:"scope"`
	Status string `json:"status"`
}

type Action struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
	Plan  string `json:"plan"`
}

type Result struct {
	Partial                  bool // The provider reached its inventory limit before examining every item.
	Status, Reason, Evidence string
	Features                 []Feature
	Actions                  []Action
	Counts                   map[string]int
	Details                  map[string]string
}

type Check struct {
	ID, Scope string
	Depends   []string
	Affects   []Feature
	Timeout   time.Duration
	Run       func(context.Context) Result
}

func Key(id, scope string) string { return id + "/" + scope }

type Finding struct {
	ID         string            `json:"id"`
	Scope      string            `json:"scope"`
	Status     string            `json:"status"`
	Reason     string            `json:"reason"`
	Evidence   string            `json:"evidence"`
	Features   []string          `json:"features"`
	BlockedBy  []string          `json:"blocked_by,omitempty"`
	DurationMS int64             `json:"duration_ms"`
	Counts     map[string]int    `json:"counts,omitempty"`
	Details    map[string]string `json:"details,omitempty"`
}

type Report struct {
	V         int       `json:"v"`
	Target    string    `json:"target"`
	Scope     string    `json:"scope"`
	CheckedAt int64     `json:"checked_at"`
	Complete  bool      `json:"complete"`
	Overall   string    `json:"overall"`
	Checks    []Finding `json:"checks"`
	Features  []Feature `json:"features"`
	Actions   []Action  `json:"actions"`
}

const MaxReportBytes = 128 * 1024

var code = regexp.MustCompile(`^[A-Za-z0-9._-]{1,96}$`)
var hash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func valid(r Result) bool {
	if !slices.Contains([]string{"pass", "warn", "fail", "blocked", "skipped", "unknown"}, r.Status) || !code.MatchString(r.Reason) || !code.MatchString(r.Evidence) || len(r.Features) > 16 || len(r.Actions) > 8 || len(r.Counts) > 12 || len(r.Details) > 12 {
		return false
	}
	for _, f := range r.Features {
		if !code.MatchString(f.ID) || !code.MatchString(f.Scope) || featureRank(f.Status) < 0 {
			return false
		}
	}
	for _, a := range r.Actions {
		if !code.MatchString(a.ID) || !code.MatchString(a.Scope) || !hash.MatchString(a.Plan) {
			return false
		}
	}
	for k, v := range r.Counts {
		if !code.MatchString(k) || v < 0 {
			return false
		}
	}
	for k, v := range r.Details {
		if !code.MatchString(k) || len(v) > 512 || !utf8.ValidString(v) || strings.IndexFunc(v, func(c rune) bool { return unicode.IsControl(c) || unicode.In(c, unicode.Bidi_Control) }) >= 0 {
			return false
		}
	}
	return true
}

func featureRank(status string) int {
	return slices.Index([]string{"not_configured", "disabled", "ready", "unknown", "degraded", "unavailable"}, status)
}

// Select the requested module and its transitive prerequisites. Validate the
// entire registry first: a cycle outside the requested scope is still a bug.
func selected(checks []Check, scope string) ([]Check, error) {
	if len(checks) > 256 {
		return nil, errors.New("too many checks")
	}
	registry := map[string]Check{}
	for _, c := range checks {
		k := Key(c.ID, c.Scope)
		if _, dup := registry[k]; dup || !code.MatchString(c.ID) || !code.MatchString(c.Scope) || c.Run == nil || len(c.Depends) > 16 {
			return nil, errors.New("invalid check registration")
		}
		registry[k] = c
	}
	state := map[string]uint8{}
	var visit func(string) error
	visit = func(k string) error {
		if state[k] == 2 {
			return nil
		}
		c, ok := registry[k]
		if !ok || state[k] == 1 {
			return errors.New("invalid check dependency")
		}
		state[k] = 1
		for _, dep := range c.Depends {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[k] = 2
		return nil
	}
	for k := range registry {
		if err := visit(k); err != nil {
			return nil, err
		}
	}
	clear(state)
	for k, c := range registry {
		group, _, _ := strings.Cut(c.ID, ".")
		if scope == "all" || scope == group {
			_ = visit(k)
		}
	}
	out := make([]Check, 0, len(state))
	for k := range state {
		out = append(out, registry[k])
	}
	sort.Slice(out, func(i, j int) bool { return Key(out[i].ID, out[i].Scope) < Key(out[j].ID, out[j].Scope) })
	return out, nil
}

type observation struct {
	result  Result
	roots   []string
	elapsed int64
	partial bool
	done    chan struct{}
}

func missing(reason string) Result {
	return Result{Status: "unknown", Reason: reason, Evidence: "none"}
}

func observe(ctx context.Context, c Check, rows map[string]*observation, slots chan struct{}) {
	row := rows[Key(c.ID, c.Scope)]
	defer close(row.done)
	for _, key := range c.Depends {
		dep := rows[key]
		<-dep.done
		switch dep.result.Status {
		case "fail", "blocked", "unknown":
			if len(dep.roots) > 0 {
				row.roots = append(row.roots, dep.roots...)
			} else {
				row.roots = append(row.roots, key)
			}
		}
	}
	if len(row.roots) > 0 {
		slices.Sort(row.roots)
		row.roots = slices.Compact(row.roots)
		row.result = Result{Status: "blocked", Reason: "dependency_unavailable", Evidence: "dependency"}
		return
	}
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		row.result = missing("cancelled")
		row.partial = true
		return
	}
	budget := c.Timeout
	if budget <= 0 {
		budget = 8 * time.Second
	}
	run, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	started := time.Now()
	completed := make(chan Result, 1)
	go func() {
		defer func() { <-slots }()
		if run.Err() == nil {
			completed <- c.Run(run)
		}
	}()
	select {
	case r := <-completed:
		if valid(r) {
			row.result = r
			row.partial = r.Partial
		} else {
			row.result = missing("invalid_evidence")
			row.partial = true
		}
	case <-run.Done():
		row.result = missing("check_timeout")
		row.partial = true
		if errors.Is(run.Err(), context.Canceled) {
			row.result = missing("cancelled")
		}
	}
	row.elapsed = time.Since(started).Milliseconds()
}

// Run bounds actual running providers, even when a provider takes time to
// unwind after cancellation. Only the coordinator publishes the final report.
func Run(ctx context.Context, checks []Check, scope, target string) (Report, error) {
	list, err := selected(checks, scope)
	if err != nil {
		return Report{}, err
	}
	r := Report{V: 1, Scope: scope, Target: target, CheckedAt: time.Now().Unix(), Complete: true, Overall: "pass", Checks: []Finding{}, Features: []Feature{}, Actions: []Action{}}
	rows := make(map[string]*observation, len(list))
	slots := make(chan struct{}, 6)
	for _, c := range list {
		rows[Key(c.ID, c.Scope)] = &observation{done: make(chan struct{})}
	}
	for _, c := range list {
		go observe(ctx, c, rows, slots)
	}
	features := map[string]Feature{}
	for _, c := range list {
		row := rows[Key(c.ID, c.Scope)]
		<-row.done
		v := row.result
		if len(v.Features) == 0 && (row.partial || v.Status == "blocked") {
			v.Features = slices.Clone(c.Affects)
			for i := range v.Features {
				v.Features[i].Status = "unknown"
			}
		}
		f := Finding{ID: c.ID, Scope: c.Scope, Status: v.Status, Reason: v.Reason, Evidence: v.Evidence, DurationMS: row.elapsed, BlockedBy: row.roots, Counts: v.Counts, Details: v.Details, Features: []string{}}
		for _, feature := range v.Features {
			k := Key(feature.ID, feature.Scope)
			if old, exists := features[k]; !exists || featureRank(feature.Status) > featureRank(old.Status) {
				features[k] = feature
			}
			f.Features = append(f.Features, feature.ID)
		}
		r.Checks = append(r.Checks, f)
		r.Actions = append(r.Actions, v.Actions...)
		if row.partial {
			r.Complete = false
		}
		switch v.Status {
		case "fail":
			r.Overall = "fail"
		case "warn":
			if r.Overall != "fail" {
				r.Overall = "warn"
			}
		case "unknown", "blocked":
			if r.Overall == "pass" {
				r.Overall = "unknown"
			}
		}
	}
	for _, f := range features {
		r.Features = append(r.Features, f)
	}
	sort.Slice(r.Features, func(i, j int) bool {
		return Key(r.Features[i].ID, r.Features[i].Scope) < Key(r.Features[j].ID, r.Features[j].Scope)
	})
	r.includeBlockedFeatures()
	r.limitOutput()
	return r, nil
}

// A root cause can affect downstream capabilities in several scopes. Keep the
// scope on the affected feature rows; coalesce labels on the root finding.
func (r *Report) includeBlockedFeatures() {
	byKey := make(map[string]*Finding, len(r.Checks))
	for i := range r.Checks {
		f := &r.Checks[i]
		byKey[Key(f.ID, f.Scope)] = f
	}
	for _, f := range r.Checks {
		for _, key := range f.BlockedBy {
			root := byKey[key]
			for _, id := range f.Features {
				if !slices.Contains(root.Features, id) {
					if len(root.Features) == 32 {
						r.Complete = false
						break
					}
					root.Features = append(root.Features, id)
				}
			}
		}
	}
}

func (r *Report) limitOutput() {
	if len(r.Features) > 256 {
		r.Features = r.Features[:256]
		r.Complete = false
	}
	if len(r.Actions) > 64 {
		r.Actions = []Action{}
		r.Complete = false
	}
	fits := func() bool { data, err := json.Marshal(r); return err == nil && len(data)+1 <= MaxReportBytes }
	if fits() {
		return
	}
	// Keep check outcomes and root causes before verbose optional fields.
	r.Complete = false
	r.Actions = []Action{}
	for i := range r.Checks {
		r.Checks[i].Details = nil
	}
	if fits() {
		return
	}
	for i := range r.Checks {
		r.Checks[i].Counts = nil
	}
	if fits() {
		return
	}
	// Unusually large future registries can still return a bounded overview
	// of every check and retain direct evidence for failures.
	for i := range r.Checks {
		r.Checks[i].Features = []string{}
		if len(r.Checks[i].BlockedBy) > 1 {
			r.Checks[i].BlockedBy = r.Checks[i].BlockedBy[:1]
		}
	}
	for !fits() && len(r.Features) > 0 {
		r.Features = r.Features[:len(r.Features)/2]
	}
	for !fits() && len(r.Checks) > 1 {
		r.Checks = r.Checks[:len(r.Checks)/2]
	}
}
