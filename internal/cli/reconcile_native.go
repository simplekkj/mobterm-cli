package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func nativeComponent(status, reason string) reconcileComponent {
	result := reconcileComponent{Component: "adapter", Status: status, Reason: reason, Activation: "unknown"}
	if status == "updated" {
		result.Activation = "restart_required"
	}
	return result
}

func nativeMigrationFailure(err error) reconcileComponent {
	switch {
	case errors.Is(err, errAdapterDisabled):
		return nativeComponent("attention", "disabled")
	case errors.Is(err, errAdapterSettings):
		return nativeComponent("attention", "ownership_conflict")
	default:
		return nativeComponent("attention", "write_failed")
	}
}

func reconcileNative(ctx context.Context, s Store, agent, executable string, probe agentProbe, check bool) reconcileComponent {
	policy, ok := agentCompatibilities[agent]
	if agent == "claude" {
		policy.minimum = minimums.Reply[agent]
	}
	if !ok || probe.status != probeOK || policy.evaluate(probe.actual) != "" {
		return nativeComponent("skipped", "unsupported")
	}
	path, err := nativeConfigPath(agent)
	if err != nil {
		return nativeComponent("attention", "ownership_conflict")
	}
	path, err = filepath.Abs(path)
	if err != nil || nativeRealParents(path) != nil {
		return nativeComponent("attention", "ownership_conflict")
	}
	_, result := planNativeReconcile(ctx, s, agent, path, executable)
	if check || result.Status != "available" {
		return result
	}

	if nativeMigrationNode(filepath.Join(s.Dir, ".lock"), true) != nil || nativeMigrationNode(path+".mobterm-lock", true) != nil {
		return nativeComponent("attention", "ownership_conflict")
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nativeComponent("attention", "write_failed")
	}
	defer unlock()
	// A legitimate pending install was written only after its config directory
	// existed. Never recreate an effective root removed by its owner.
	if nativeMigrationNode(path+".mobterm-lock", true) != nil {
		return nativeComponent("attention", "ownership_conflict")
	}
	configUnlock, err := lockFile(ctx, path+".mobterm-lock")
	if err != nil {
		return nativeComponent("attention", "write_failed")
	}
	defer configUnlock()

	plan, result := planNativeReconcile(ctx, s, agent, path, executable)
	if result.Status != "available" {
		return result
	}
	if err = plan.apply(ctx, s); err != nil {
		return nativeMigrationFailure(err)
	}
	if plan.recovering {
		// An earlier compatible transaction can finish with an old revision
		// or without the current host token. Compare the committed state again.
		plan, result = planNativeReconcile(ctx, s, agent, path, executable)
		if result.Status == "available" {
			if err = plan.apply(ctx, s); err != nil {
				return nativeMigrationFailure(err)
			}
		} else if result.Status != "current" {
			return result
		}
	}
	return nativeComponent("updated", "updated")
}

type nativeReconcilePlan struct {
	next           nativeHookRecord
	files          map[string][]byte
	config, result []byte
	configExists   bool
	mode           os.FileMode
	recovering     bool
}

func (p nativeReconcilePlan) apply(ctx context.Context, s Store) error {
	if !p.recovering && p.next.HostToken == "" {
		p.next.HostToken = randomID()
		files, _, err := renderNativeAssets(s, p.next.Agent, p.next.Path, p.next.Executable, p.next.HostToken)
		if err != nil {
			return err
		}
		p.files = files
		for path, data := range files {
			file := p.next.Files[path]
			file.SHA = nativeDigest(data)
			p.next.Files[path] = file
		}
	}
	return commitNativeInstall(ctx, s, p.next, p.files, p.config, p.result, p.configExists, p.mode, p.recovering)
}

// Directory write access controls replacement of a regular file. Walk the
// chain as far as the filesystem root; permit a root-owned sticky temporary
// ancestor only above an existing, account-owned private directory.
func reconcileSafeParents(path string) error {
	if err := nativeRealParents(path); err != nil {
		return err
	}
	uid := uint32(os.Geteuid())
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir) // nativeRealParents admits only the system aliases.
		if err != nil {
			return err
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || (owner.Uid != uid && owner.Uid != 0) {
			return errors.New("unsafe migration directory")
		}
		if dir == filepath.Dir(path) {
			if owner.Uid != uid || info.Mode().Perm()&0022 != 0 {
				return errors.New("unsafe migration directory")
			}
		} else if info.Mode().Perm()&0022 != 0 && !(owner.Uid == 0 && info.Mode()&os.ModeSticky != 0) {
			return errors.New("writable migration ancestor")
		}
		if filepath.Dir(dir) == dir {
			return nil
		}
	}
}

// Use the same effective UID as the private ownership record. A replacement
// file from another account is not adopted even if its contents happen to match.
func nativeMigrationNode(path string, private bool) error {
	if err := reconcileSafeParents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	disallowed := os.FileMode(0022)
	if private {
		disallowed = 0077
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || !info.Mode().IsRegular() || info.Mode().Perm()&disallowed != 0 {
		return errors.New("unsafe native migration file")
	}
	return nil
}

// Read all evidence before preparing any intent. A committed record proves
// only matching bytes still present. Missing files may be created solely when
// an existing install journal proves that absence was its recorded pre-state.
func planNativeReconcile(ctx context.Context, s Store, agent, path, executable string) (nativeReconcilePlan, reconcileComponent) {
	var p nativeReconcilePlan
	conflict := nativeComponent("attention", "ownership_conflict")
	pendingConflict := nativeComponent("attention", "pending_conflict")
	found, err := reconcileCandidate(s, agent, "adapter")
	if err != nil {
		return p, conflict
	}
	if !found {
		return p, nativeComponent("skipped", "unmanaged")
	}
	var r nativeHookRecord
	if s.readJSON(nativeRecordName(agent), &r) != nil {
		return p, conflict
	}
	if r.Pending != "" {
		conflict = pendingConflict
	}
	if nativeValidateRecord(s, r, agent) != nil || path != r.Path || r.Executable != executable || r.Revision > nativeAdapterRevision ||
		(r.HostToken != "" && !opaqueID.MatchString(r.HostToken)) {
		return p, conflict
	}
	// Reconciliation has no authority to uninstall. An unfinished uninstall
	// is left for the explicit operation which originally requested removal.
	if r.Pending == "uninstall" || (r.ReconcileVersion != "" && r.ReconcileVersion != version) {
		return p, pendingConflict
	}
	if err := checkNativeSettings(ctx, agent, path); err != nil {
		return p, nativeMigrationFailure(err)
	}
	p.recovering = r.Pending == "install"
	p.next = r
	changed := p.recovering || r.Revision != nativeAdapterRevision
	if !p.recovering {
		p.next.Revision = nativeAdapterRevision
		p.next.Pending = "install"
		p.next.ReconcileVersion = version
		p.next.ReferenceRequired = true
		p.next.Files = make(map[string]nativeOwnedFile, len(r.Files))
		if r.HostToken == "" {
			// Never create a host credential merely to perform a check. Mark it
			// as needed; apply renders the final targets under both locks.
			changed = true
		}
	}
	p.files, _, err = renderNativeAssets(s, agent, path, executable, r.HostToken)
	if err != nil {
		return p, conflict
	}
	for asset, file := range r.Files {
		if !bodyHashPattern.MatchString(file.SHA) || (file.PreviousSHA != "" && (!p.recovering || !bodyHashPattern.MatchString(file.PreviousSHA))) {
			return p, conflict
		}
		target := nativeDigest(p.files[asset])
		if p.recovering && target != file.SHA {
			return p, pendingConflict
		}
		if nativeMigrationNode(asset, true) != nil {
			return p, conflict
		}
		data, exists, _, err := nativeRead(asset)
		if err != nil {
			return p, conflict
		}
		if !exists && !p.recovering {
			return p, nativeComponent("attention", "disabled")
		}
		if !nativeAllowed(data, exists, file, r.Pending) {
			return p, conflict
		}
		if !p.recovering {
			p.next.Files[asset] = nativeOwnedFile{SHA: target, PreviousSHA: file.SHA}
			changed = changed || target != file.SHA
		}
	}
	if agent == "opencode" {
		if nativeMigrationNode(path, false) != nil {
			return p, conflict
		}
		p.config, p.configExists, p.mode, err = nativeRead(path)
		if err != nil || nativeOtherOpenCodeInstallation(ctx, p.config, r.Reference) != nil {
			return p, conflict
		}
		p.result, err = editNativePlugin(p.config, r.Reference, "install", true, p.recovering && !r.ReferenceRequired)
		if errors.Is(err, errNativeReferenceRemoved) {
			return p, nativeComponent("attention", "disabled")
		}
		if err != nil {
			return p, conflict
		}
	}
	if changed {
		if nativeMigrationNode(filepath.Join(s.Dir, ".lock"), true) != nil || nativeMigrationNode(path+".mobterm-lock", true) != nil {
			return p, conflict
		}
		return p, nativeComponent("available", "template_changed")
	}
	return p, nativeComponent("current", "current")
}
