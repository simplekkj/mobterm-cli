package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/simplekkj/mobterm-cli/internal/doctor"
	"golang.org/x/sys/unix"
)

const doctorActionID = "agent.reconcile"

type doctorOptions struct {
	Scope, Agent, Pair string
	Offline, Deep      bool
}

func doctorNode(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("unknown file identity")
	}
	return fmt.Sprintf("%d.%d.%d.%d.%d.%d", st.Dev, st.Ino, st.Uid, info.Mode(), info.ModTime().UnixNano(), info.Size()), nil
}

// The store directory's device/inode survives our own locks and journals.
// The installation file also needs ctime: renaming the original inode back
// into place can preserve its contents, inode number and modification time.
func doctorInstallationGeneration(s Store) (string, error) {
	if err := doctorDirectory(s.Dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "store_absent", nil
		}
		return "", err
	}
	var dir unix.Stat_t
	if err := unix.Lstat(s.Dir, &dir); err != nil {
		return "", err
	}
	if dir.Mode&unix.S_IFMT != unix.S_IFDIR || dir.Uid != uint32(os.Geteuid()) || dir.Mode&0077 != 0 {
		return "", errDoctorUnsafe
	}
	storeID := fmt.Sprintf("%d.%d", dir.Dev, dir.Ino)
	path := filepath.Join(s.Dir, "installation.json")
	f, err := doctorPrivateFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return storeID + ".absent", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()

	var before, current unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &before); err != nil {
		return "", err
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", err
	}
	var identity storedInstallation
	if len(raw) > 4096 || json.Unmarshal(raw, &identity) != nil || identity.V != 1 || !opaqueID.MatchString(identity.ID) {
		return "", errDoctorUnsafe
	}
	// Confirm the pathname still identifies the exact version whose bytes we
	// decoded. Reading on the original handle alone cannot see an atomic swap.
	if err := unix.Lstat(path, &current); err != nil {
		return "", err
	}
	if current.Dev != before.Dev || current.Ino != before.Ino || current.Ctim != before.Ctim {
		return "", errDoctorUnsafe
	}
	return fmt.Sprintf("%s.%d.%d.%d.%d.%s", storeID, before.Dev, before.Ino, before.Ctim.Sec, before.Ctim.Nsec, identity.ID), nil
}

// An unsafe or corrupt identity can still be diagnosed, but cannot authorize
// a repair. Only an opaque digest leaves this process.
func doctorTarget(s Store, executable string) (string, bool) {
	host, eHost := os.Hostname()
	node, eNode := doctorNode(executable)
	generation, eStore := doctorInstallationGeneration(s)
	safe := s.Dir != "" && executable != "" && eHost == nil && eNode == nil && node != "absent" && eStore == nil
	data, _ := json.Marshal([]any{"doctor.v1", host, os.Geteuid(), s.Dir, generation, executable, node, version, commit})
	return nativeDigest(data), safe
}

type doctorComponent struct{ agent, kind string }

func (c doctorComponent) scope() string { return c.agent + "." + c.kind }
func componentFor(scope string) (doctorComponent, bool) {
	agent, kind, ok := strings.Cut(scope, ".")
	for _, spec := range reconcileAgents {
		if ok && agent == spec.name && slices.Contains(spec.components, kind) {
			return doctorComponent{agent, kind}, true
		}
	}
	return doctorComponent{}, false
}

func (c doctorComponent) plan(ctx context.Context, s Store, exe string, p agentProbe) (reconcileComponent, any, []string) {
	if c.kind == "hooks" {
		path, err := configPath(c.agent)
		if err != nil {
			return classicAttention("ownership_conflict"), nil, nil
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return classicAttention("ownership_conflict"), nil, nil
		}
		tx, _, row := planClassicReconcile(s, c.agent, path, exe, p)
		return row, tx, c.classicFiles(s, path)
	}
	policy := agentCompatibilities[c.agent]
	if c.agent == "claude" {
		policy.minimum = minimums.Reply[c.agent]
	}
	if policy.evaluate(p.actual) != "" {
		return nativeComponent("skipped", "unsupported"), nil, nil
	}
	path, err := nativeConfigPath(c.agent)
	if err != nil {
		return nativeComponent("attention", "ownership_conflict"), nil, nil
	}
	path, err = filepath.Abs(path)
	if err != nil || nativeRealParents(path) != nil {
		return nativeComponent("attention", "ownership_conflict"), nil, nil
	}
	plan, row := planNativeReconcile(ctx, s, c.agent, path, exe)
	return row, c.nativeEvidence(plan), c.nativeFiles(s, path, plan)
}

func (c doctorComponent) classicFiles(s Store, path string) []string {
	files := []string{path, filepath.Join(s.Dir, "hooks-"+c.agent+".json"), filepath.Join(s.Dir, hookTransactionName(c.agent))}
	if c.agent == "codex" {
		files = append(files, filepath.Join(filepath.Dir(path), "hooks.json"))
	}
	return files
}

func (c doctorComponent) nativeFiles(s Store, path string, p nativeReconcilePlan) []string {
	files := []string{path, filepath.Join(s.Dir, nativeRecordName(c.agent))}
	for name := range p.next.Files {
		files = append(files, name)
	}
	if c.agent == "pi" {
		files = append(files, filepath.Join(filepath.Dir(filepath.Dir(path)), "settings.json"))
	}
	sort.Strings(files)
	return slices.Compact(files)
}

func (c doctorComponent) nativeEvidence(p nativeReconcilePlan) any {
	return []any{p.next, nativeDigest(p.config), nativeDigest(p.result), p.configExists, p.recovering, p.mode}
}

// The engine generates no persistent grant. A token covers this process's
// effective target, the live plan, and the generation of every relevant file.
// Replacing and restoring the same contents invalidates a pending plan.
func (c doctorComponent) fingerprint(s Store, exe string, p agentProbe, plan any, files []string, epoch int64) string {
	target, safe := doctorTarget(s, exe)
	if !safe {
		return ""
	}
	h := sha256.New()
	enc := json.NewEncoder(h)
	if enc.Encode([]any{"repair.v1", target, c.scope(), p.actual, epoch, plan}) != nil {
		return ""
	}
	for _, path := range files {
		stamp, err := doctorNode(path)
		if err != nil {
			return ""
		}
		if enc.Encode([]string{path, stamp}) != nil {
			return ""
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c doctorComponent) inspect(ctx context.Context, s Store, exe string, p agentProbe, offline bool) doctor.Result {
	scope := c.scope()
	row := reconcileComponent{Reason: "unmanaged", Status: "skipped"}
	var plan any
	var files []string
	exists, err := reconcileCandidate(s, c.agent, c.kind)
	switch {
	case err != nil:
		row = classicAttention("ownership_conflict")
	case !exists:
	case offline:
		row = reconcileComponent{Status: "unknown", Reason: "version_unconfirmed"}
	case p.status == probeNotFound:
		row = classicAttention("missing_agent")
	case p.status != probeOK || p.reason == "unknown_output":
		row = reconcileComponent{Status: "unknown", Reason: "probe_failed"}
	case p.reason != "":
		row = classicAttention("unsupported")
	default:
		row, plan, files = c.plan(ctx, s, exe, p)
	}
	status, feature := "warn", "degraded"
	reason := row.Reason
	switch {
	case row.Status == "current":
		status, feature = "pass", "ready"
		if row.Activation == "native_review_required" {
			reason = row.Activation
		}
	case row.Reason == "disabled":
		status, feature = "skipped", "disabled"
	case row.Reason == "unmanaged":
		status, feature = "skipped", "not_configured"
	case row.Status == "unknown":
		status, feature = "unknown", "unknown"
	case row.Reason == "ownership_conflict" || row.Reason == "pending_conflict":
		status, feature = "fail", "unavailable"
	}
	r := doctor.Result{Status: status, Reason: reason, Evidence: "disk", Features: []doctor.Feature{{ID: "agent.configuration", Scope: scope, Status: feature}}}
	if exists {
		r.Features = append(r.Features, doctor.Feature{ID: "agent.runtime", Scope: scope, Status: "unknown"})
	}
	if row.Status == "available" {
		generation := c.fingerprint(s, exe, p, plan, files, time.Now().Unix()/300)
		if generation != "" {
			r.Actions = []doctor.Action{{ID: doctorActionID, Scope: scope, Plan: generation}}
		}
	}
	return r
}

func doctorAgentChecks(s Store, exe string, o doctorOptions) []doctor.Check {
	var checks []doctor.Check
	for _, spec := range reconcileAgents {
		if o.Agent != "" && o.Agent != spec.name {
			continue
		}
		agent := spec.name
		// Only the discovery check writes this value. Its done channel publishes
		// it to dependent component checks after a successful discovery.
		var probe agentProbe
		checks = append(checks, doctor.Check{ID: "agent.discovery", Scope: agent, Timeout: agentProbeTimeout + time.Second, Run: func(ctx context.Context) doctor.Result {
			_, err := exec.LookPath(agent)
			if err != nil {
				probe = agentProbe{status: probeNotFound}
				return doctor.Result{Status: "skipped", Reason: "missing_agent", Evidence: "exec_path"}
			}
			r := doctor.Result{Status: "pass", Reason: "agent_found", Evidence: "exec_path"}
			needsVersion := o.Deep
			for _, kind := range spec.components {
				if exists, _ := reconcileCandidate(s, agent, kind); exists {
					needsVersion = true
				}
			}
			if !o.Offline && needsVersion {
				probe = probeAgent(ctx, agent)
				if probe.status != probeOK || probe.reason == "unknown_output" {
					r.Status, r.Reason = "warn", "probe_failed"
				} else if probe.reason != "" {
					r.Status, r.Reason = "warn", "unsupported"
				} else {
					r.Evidence = "version_probe"
				}
			}
			return r
		}})
		for _, kind := range spec.components {
			component := doctorComponent{agent, kind}
			checks = append(checks, doctor.Check{ID: "agent.integration", Scope: component.scope(), Depends: []string{doctor.Key("agent.discovery", agent)}, Affects: []doctor.Feature{{ID: "agent.configuration", Scope: component.scope()}}, Run: func(ctx context.Context) doctor.Result {
				return component.inspect(ctx, s, exe, probe, o.Offline)
			}})
		}
	}
	return checks
}

type doctorRepairRequest struct {
	V      int    `json:"v"`
	ID     string `json:"id"`
	Scope  string `json:"scope"`
	Target string `json:"target"`
	Plan   string `json:"plan"`
}
type doctorRepairReceipt struct {
	V      int    `json:"v"`
	Status string `json:"status"`
	Scope  string `json:"scope"`
}

func (req doctorRepairRequest) valid() bool {
	_, ok := componentFor(req.Scope)
	return ok && req.V == 1 && req.ID == doctorActionID && bodyHashPattern.MatchString(req.Target) && bodyHashPattern.MatchString(req.Plan)
}

func doctorRepair(ctx context.Context, s Store, exe string, req doctorRepairRequest) doctorRepairReceipt {
	c, _ := componentFor(req.Scope)
	r := doctorRepairReceipt{V: 1, Scope: req.Scope, Status: "stale"}
	target, safe := doctorTarget(s, exe)
	if !req.valid() || !safe || target != req.Target {
		return r
	}
	if exists, err := reconcileCandidate(s, c.agent, c.kind); err != nil || !exists {
		return r
	}
	p := probeAgent(ctx, c.agent)
	if p.status != probeOK || p.reason != "" {
		return r
	}
	return applyDoctorPlan(ctx, s, exe, c, p, req)
}

func applyDoctorPlan(ctx context.Context, s Store, exe string, c doctorComponent, p agentProbe, req doctorRepairRequest) doctorRepairReceipt {
	r := doctorRepairReceipt{V: 1, Status: "stale", Scope: c.scope()}
	matches := func(plan any, files []string) bool {
		if ctx.Err() != nil {
			return false
		}
		target, safe := doctorTarget(s, exe)
		if !safe || target != req.Target {
			return false
		}
		epoch := time.Now().Unix() / 300
		return c.fingerprint(s, exe, p, plan, files, epoch) == req.Plan || c.fingerprint(s, exe, p, plan, files, epoch-1) == req.Plan
	}
	row, plan, files := c.plan(ctx, s, exe, p)
	if row.Status != "available" || !matches(plan, files) {
		return r
	}
	started := false
	if c.kind == "hooks" {
		row = reconcileClassicAuthorized(ctx, s, c.agent, exe, p, false, func(tx hookTransaction) bool {
			started = matches(tx, c.classicFiles(s, tx.Path))
			return started
		})
	} else {
		row = reconcileNativeAuthorized(ctx, s, c.agent, exe, p, false, func(plan nativeReconcilePlan) bool {
			started = matches(c.nativeEvidence(plan), c.nativeFiles(s, plan.next.Path, plan))
			return started
		})
	}
	if !started {
		if row.Reason == "write_failed" {
			r.Status = "failed"
		}
		return r
	}
	r.Status = "unknown"
	if ctx.Err() != nil || row.Status != "updated" {
		return r
	}
	fresh, _, _ := c.plan(ctx, s, exe, p)
	target, safe := doctorTarget(s, exe)
	if !safe || target != req.Target || ctx.Err() != nil {
		return r
	}
	switch fresh.Status {
	case "current":
		r.Status = "manual_required" // Agent restart or trust review cannot be verified over exec.
	case "available":
		r.Status = "applied" // One compatible journal completed. A new plan needs its own confirmation.
	}
	return r
}
