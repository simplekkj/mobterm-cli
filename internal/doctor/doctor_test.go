package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndependentProviderAndDependencyRoots(t *testing.T) {
	var calls atomic.Int32
	fail := func(context.Context) Result {
		calls.Add(1)
		return Result{Status: "fail", Reason: "tls_failure", Evidence: "transport"}
	}
	blocked := func(context.Context) Result { t.Error("ran dependent"); return Result{} }
	checks := []Check{
		{ID: "network.service", Scope: "a", Run: fail},
		{ID: "pair.state", Scope: "a", Depends: []string{"network.service/a"}, Run: blocked},
		{ID: "new.business", Scope: "a", Depends: []string{"network.service/a", "pair.state/a"}, Affects: []Feature{{ID: "new.feature", Scope: "a"}}, Run: blocked},
		{ID: "new.business", Scope: "b", Run: func(context.Context) Result { return Result{Status: "pass", Reason: "working", Evidence: "service"} }},
		{ID: "unused.check", Scope: "host", Run: blocked},
	}
	r, err := Run(t.Context(), checks, "new", "target")
	if err != nil || r.Overall != "fail" || !r.Complete || len(r.Checks) != 4 || calls.Load() != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	if !slices.Equal(r.Checks[1].BlockedBy, []string{"network.service/a"}) || r.Checks[2].Status != "pass" || !slices.Equal(r.Features, []Feature{{"new.feature", "a", "unknown"}}) {
		t.Fatal(r)
	}
}

func TestRegistrationAndTimeout(t *testing.T) {
	ok := func(context.Context) Result { return Result{Status: "pass", Reason: "available", Evidence: "disk"} }
	a := Check{ID: "a.test", Scope: "host", Run: ok}
	for _, bad := range [][]Check{{a, a}, {{ID: "b.test", Scope: "host", Depends: []string{"a.test/host"}, Run: ok}}, {{ID: "a.test", Scope: "host", Depends: []string{"a.test/host"}, Run: ok}}} {
		if _, err := Run(t.Context(), bad, "all", "target"); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	stop := make(chan struct{})
	defer close(stop)
	r, err := Run(t.Context(), []Check{a, {ID: "b.slow", Scope: "host", Timeout: 15 * time.Millisecond, Run: func(context.Context) Result { <-stop; return Result{} }}}, "all", "target")
	if err != nil || r.Complete || r.Checks[0].Status != "pass" || r.Checks[1].Reason != "check_timeout" {
		t.Fatal(r, err)
	}
}

func TestBoundedPartialReportAndRootImpact(t *testing.T) {
	checks := []Check{}
	for i := 0; i < 230; i++ {
		scope := fmt.Sprintf("pair.%d", i)
		checks = append(checks, Check{ID: "pair.local", Scope: scope, Run: func(context.Context) Result {
			return Result{Status: "pass", Reason: "valid", Evidence: "disk", Details: map[string]string{"path": strings.Repeat("a", 510)}, Features: []Feature{{"pair.state", scope, "ready"}, {"notifications", scope, "unknown"}}}
		}})
	}
	checks = append(checks, Check{ID: "network.api", Scope: "host", Run: func(context.Context) Result { return Result{Status: "fail", Reason: "tls_error", Evidence: "network"} }},
		Check{ID: "pair.remote", Scope: "pair.0", Depends: []string{"network.api/host"}, Affects: []Feature{{ID: "job.notification", Scope: "pair.0"}}, Run: func(context.Context) Result { t.Error("blocked"); return Result{} }})
	r, err := Run(t.Context(), checks, "all", "target")
	raw, _ := json.Marshal(r)
	if err != nil || r.Complete || r.Overall != "fail" || len(r.Checks) != len(checks) || len(r.Features) > 256 || len(raw)+1 > MaxReportBytes ||
		!slices.Equal(r.Checks[0].Features, []string{"job.notification"}) {
		t.Fatal(err, len(raw), r.Checks[0])
	}
}
