package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/mobterm/mobterm-cli/internal/doctor"
	"golang.org/x/term"
)

const doctorHost = "host"

func doctorFact(status, reason, evidence string) doctor.Result {
	return doctor.Result{Status: status, Reason: reason, Evidence: evidence}
}

func doctorFeature(id, scope, status string) doctor.Feature {
	return doctor.Feature{ID: id, Scope: scope, Status: status}
}

func doctorProviders(s Store, a API, o doctorOptions) []doctor.Check {
	// Registration uses only the bounded, private pair catalog to form the
	// graph. Recheck its authority before making an authenticated request.
	var catalog doctorCatalog
	if o.Scope != "core" && o.Scope != "agent" {
		catalog = doctorReadCatalog(s)
	}
	private := doctor.Key("storage.directory", doctorHost)
	identity := doctor.Key("storage.identity", doctorHost)
	checks := []doctor.Check{
		{ID: "core.executable", Scope: doctorHost, Run: doctorExecutable},
		{ID: "core.environment", Scope: doctorHost, Run: doctorEnvironment},
		{ID: "storage.directory", Scope: doctorHost, Run: func(context.Context) doctor.Result {
			switch {
			case errors.Is(catalog.dirErr, os.ErrNotExist):
				return doctorFact("skipped", "store_not_configured", "filesystem")
			case catalog.dirErr != nil:
				return doctorFact("fail", "private_directory_required", "filesystem")
			}
			return doctorFact("pass", "private_directory", "filesystem")
		}},
		{ID: "storage.identity", Scope: doctorHost, Depends: []string{private}, Run: func(context.Context) doctor.Result {
			if errors.Is(catalog.identityErr, os.ErrNotExist) && len(catalog.pairs) == 0 {
				return doctorFact("skipped", "identity_not_configured", "filesystem")
			}
			if catalog.identityErr != nil {
				return doctorFact("fail", "installation_identity_invalid", "filesystem")
			}
			return doctorFact("pass", "installation_identity_valid", "filesystem")
		}},
		{ID: "storage.pairs", Scope: doctorHost, Depends: []string{private}, Run: func(context.Context) doctor.Result {
			if catalog.pairsErr != nil {
				return doctorFact("fail", "pair_store_invalid", "filesystem")
			}
			r := doctorFact("pass", "pair_catalog_read", "filesystem")
			if len(catalog.pairs) == 0 {
				r.Status, r.Reason = "skipped", "pair_not_configured"
			}
			r.Counts = map[string]int{"pairs": len(catalog.pairs)}
			return r
		}},
		{ID: "storage.capacity", Scope: doctorHost, Depends: []string{private}, Run: func(context.Context) doctor.Result {
			var stat syscall.Statfs_t
			if err := syscall.Statfs(s.Dir, &stat); err != nil {
				return doctorFact("skipped", "capacity_unobserved", "filesystem")
			}
			if stat.Bavail == 0 {
				return doctorFact("fail", "filesystem_full", "filesystem")
			}
			return doctorFact("pass", "space_available", "filesystem")
		}},
		{ID: "storage.lock", Scope: doctorHost, Depends: []string{private}, Run: func(context.Context) doctor.Result {
			return doctorLock(s)
		}},
		{ID: "workspace.target", Scope: doctorHost, Run: func(ctx context.Context) doctor.Result {
			return doctorWorkspaceTarget(ctx, o.Deep && !o.Offline)
		}},
		{ID: "job.shell", Scope: doctorHost, Run: func(ctx context.Context) doctor.Result {
			return doctorJobShell(ctx, o.Offline)
		}},
		{ID: "job.records", Scope: doctorHost, Depends: []string{identity}, Run: func(ctx context.Context) doctor.Result {
			return doctorJobs(ctx, s, catalog.installation, o)
		}},
		{ID: "delivery.outbox", Scope: doctorRequestedScope(o), Depends: []string{private}, Run: func(ctx context.Context) doctor.Result {
			return doctorOutbox(ctx, s, o, catalog)
		}},
	}
	for _, name := range []string{"tmux", "herdr"} {
		checks = append(checks, doctor.Check{ID: "workspace.executable", Scope: name, Run: func(ctx context.Context) doctor.Result {
			return doctorWorkspaceTool(ctx, name, o.Deep && !o.Offline)
		}})
	}
	return append(checks, doctorPairChecks(s, a, catalog, o)...)
}

func doctorSafeDetail(s string) bool {
	return s != "" && len(s) <= 512 && utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	}) < 0
}

func doctorExecutable(context.Context) doctor.Result {
	r := doctorFact("pass", "executable_verified", "process")
	r.Details = map[string]string{"os": runtime.GOOS, "arch": runtime.GOARCH}
	if doctorSafeDetail(version) {
		r.Details["cli_version"] = version
	}
	r.Counts = map[string]int{"protocol": 1}
	exe, err := os.Executable()
	if err != nil {
		r.Status, r.Reason = "unknown", "executable_unobserved"
		return r
	}
	st, err := os.Stat(exe)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
		r.Status, r.Reason = "fail", "executable_unavailable"
		return r
	}
	if doctorSafeDetail(exe) {
		r.Details["cli_path"] = exe
	}
	seen := []os.FileInfo{st}
	for i, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if i == 256 {
			r.Status, r.Reason, r.Partial = "warn", "path_scan_limited", true
			break
		}
		other, err := os.Stat(filepath.Join(dir, "mobterm-cli"))
		if err != nil || !other.Mode().IsRegular() || other.Mode().Perm()&0111 == 0 {
			continue
		}
		duplicate := false
		for _, found := range seen {
			if os.SameFile(found, other) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			seen = append(seen, other)
		}
	}
	r.Counts["distinct_installations"] = len(seen)
	if len(seen) > 1 {
		r.Status, r.Reason = "warn", "multiple_cli_installations"
	}
	return r
}

func doctorEnvironment(context.Context) doctor.Result {
	r := doctorFact("pass", "execution_environment", "process")
	home, err := os.UserHomeDir()
	info, statErr := os.Stat(home)
	if err != nil || !filepath.IsAbs(home) || statErr != nil || !info.IsDir() {
		r.Status, r.Reason = "warn", "home_unavailable"
	} else if os.Getenv("PATH") == "" {
		r.Status, r.Reason = "warn", "path_unavailable"
	}
	kind := "non_pty"
	if term.IsTerminal(int(os.Stdin.Fd())) {
		kind = "terminal"
	}
	r.Details = map[string]string{"execution_context": kind}
	return r
}

var errDoctorUnsafe = errors.New("unsafe doctor input")

func doctorDirectory(dir string) error {
	if !filepath.IsAbs(dir) || nativeRealParents(filepath.Join(dir, "ownership")) != nil {
		return errDoctorUnsafe
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return errDoctorUnsafe
	}
	return reconcileSafeParents(filepath.Join(dir, "ownership"))
}

// Check authority and permissions on the same descriptor that supplies the
// bytes. A diagnostic must neither open a FIFO nor follow a config symlink.
func doctorPrivateFile(path string) (*os.File, error) {
	if err := doctorDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Geteuid()) {
			err = errDoctorUnsafe
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func doctorReadJSON(path string, limit int64, out any) error {
	f, err := doctorPrivateFile(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > limit {
		return errDoctorUnsafe
	}
	return json.Unmarshal(b, out)
}

func doctorLock(s Store) doctor.Result {
	f, err := doctorPrivateFile(filepath.Join(s.Dir, ".lock"))
	if errors.Is(err, os.ErrNotExist) {
		return doctorFact("skipped", "no_existing_lock", "filesystem")
	}
	if err != nil {
		return doctorFact("warn", "lock_file_invalid", "filesystem")
	}
	defer f.Close()
	// A nonblocking shared lock on the existing inode observes an exclusive
	// writer without creating a lock file or queueing behind that writer.
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return doctorFact("warn", "store_busy", "filesystem")
	}
	if err != nil {
		return doctorFact("unknown", "lock_unobserved", "filesystem")
	}
	return doctorFact("pass", "lock_available", "filesystem")
}

func doctorWorkspaceTool(ctx context.Context, name string, deep bool) doctor.Result {
	path, err := exec.LookPath(name)
	if err != nil {
		return doctorFact("skipped", "tool_not_found", "process_path")
	}
	r := doctorFact("pass", "tool_on_path", "process_path")
	if doctorSafeDetail(path) {
		r.Details = map[string]string{"executable": path}
	}
	if !deep {
		return r
	}
	arg := "--version"
	if name == "tmux" {
		arg = "-V"
	}
	output, err := commandOutput(ctx, path, arg)
	token, valid := strings.CutPrefix(strings.TrimSpace(string(output)), name+" ")
	if err != nil || !valid || len(token) > 64 {
		r.Status, r.Reason = "unknown", "tool_version_unknown"
		return r
	}
	if name == "tmux" {
		err = checkTmuxVersion(token)
	} else {
		err = checkHerdrVersion(token)
	}
	if err != nil {
		r.Status, r.Reason = "warn", targetErrorCode(err)
	} else if doctorSafeDetail(token) {
		if r.Details == nil {
			r.Details = map[string]string{}
		}
		r.Details["version"] = token
	}
	return r
}

func doctorWorkspaceTarget(ctx context.Context, deep bool) doctor.Result {
	r := doctorFact("skipped", "no_foreground_evidence", "process")
	r.Features = []doctor.Feature{doctorFeature("workspace.navigation", doctorHost, "unknown")}
	if !deep || !term.IsTerminal(int(os.Stdin.Fd())) {
		return r
	}
	if os.Getenv("TMUX_PANE") == "" && os.Getenv("HERDR_PANE_ID") == "" {
		return r
	}
	backend, ok := doctorPaneShell(ctx)
	if !ok {
		r.Status, r.Reason = "unknown", "target_unconfirmed"
		return r
	}
	r.Status, r.Reason, r.Evidence = "pass", "workspace_shell_verified", "runtime"
	r.Details = map[string]string{"backend": backend}
	// Owning this pane is narrower than a proven phone navigation.
	return r
}

// Read only the explicitly inherited pane. Verify that its shell owns the
// caller and that the socket did not change around the query. A detached SSH
// exec with another terminal's environment cannot assert that pane as current.
func doctorPaneShell(ctx context.Context) (string, bool) {
	tm, hp := os.Getenv("TMUX"), os.Getenv("HERDR_PANE_ID")
	if (tm == "") == (hp == "") {
		return "", false
	}
	backend, path, pane := "herdr", "", hp
	if tm != "" {
		backend, pane = "tmux", os.Getenv("TMUX_PANE")
		parts := strings.Split(tm, ",")
		if len(parts) < 3 || !tmuxPane.MatchString(pane) {
			return "", false
		}
		path = strings.Join(parts[:len(parts)-2], ",")
	} else {
		ns := os.Getenv("HERDR_SESSION")
		if ns == "" {
			ns = "default"
		}
		var err error
		path, err = herdrSocket(ns)
		if err != nil || !simpleID.MatchString(pane) || os.Getenv("HERDR_SOCKET_PATH") != "" && os.Getenv("HERDR_SOCKET_PATH") != path {
			return "", false
		}
	}
	if !filepath.IsAbs(path) || !doctorSafeDetail(path) || nativeRealParents(path) != nil {
		return "", false
	}
	info, err := targetSocketInfo(path)
	if err != nil {
		return "", false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return "", false
	}
	before, err := socketIdentity(path)
	if err != nil {
		return "", false
	}
	var shellPID int
	if backend == "herdr" {
		shellPID, err = herdrShellPID(ctx, path, pane)
	} else {
		// -N refuses to start any server; display-message -p never updates the
		// target's status line and restricts the format to this exact pane.
		var raw []byte
		raw, err = commandOutput(ctx, "tmux", "-N", "-S", path, "display-message", "-p", "-t", pane, "#{socket_path}\t#{pane_id}\t#{pane_pid}\t#{version}")
		fields := strings.Split(strings.TrimSpace(string(raw)), "\t")
		if err != nil || len(fields) != 4 || filepath.Clean(fields[0]) != filepath.Clean(path) || fields[1] != pane || checkTmuxVersion(fields[3]) != nil {
			return "", false
		}
		shellPID, err = strconv.Atoi(fields[2])
	}
	if err != nil || shellPID <= 1 || !codexIsAncestor(shellPID) {
		return "", false
	}
	after, err := socketIdentity(path)
	return backend, err == nil && before == after
}
