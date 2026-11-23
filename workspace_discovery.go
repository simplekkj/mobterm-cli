package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxDiscoveryOutput = 4 * 1024 * 1024
const maxDiscoverySessions = 64
const maxPanesPerSession = 4096

var workspaceOperations = []string{"tmux.discovery", "herdr.list", "herdr.probe", "herdr.snapshot"}

func readCapabilities(s Store) (map[string]any, error) {
	result := map[string]any{
		"v": 1, "version": version, "commit": commit, "operations": workspaceOperations,
		"server_time": time.Now().Unix(), "execution_backends": []string{},
		"quick_command_result_protocols": []int{1},
		"limits": map[string]int{"frame_bytes": maxFrame, "response_bytes": maxDiscoveryOutput,
			"tmux_sessions": maxDiscoverySessions, "panes_per_session": maxPanesPerSession},
	}
	if _, err := quickAccountShell(context.Background()); err == nil {
		result["execution_backends"] = []string{quickNativeBackend}
		result["quick_command_foreground_protocols"] = []int{1}
	}
	id, err := s.existingInstallation()
	if err == nil {
		result["installation_id"] = id
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("installation_unavailable")
	}
	return result, nil
}

type workspaceQuery struct {
	V          int    `json:"v"`
	Executable string `json:"executable"`
	Socket     string `json:"socket,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
}

func discoverWorkspace(ctx context.Context, operation string, in io.Reader) (map[string]any, error) {
	if !slices.Contains(workspaceOperations, operation) {
		return nil, errors.New("invalid_request")
	}
	var q workspaceQuery
	// SSH exec sends EOF after the frame. Include the trailing-byte check in
	// the same deadline as the header and body.
	done := make(chan error, 1)
	go func() {
		err := readFrame(in, &q)
		if err == nil {
			var trailing [1]byte
			_, err = io.ReadFull(in, trailing[:])
			if errors.Is(err, io.EOF) {
				err = nil
			} else {
				err = errors.New("invalid_request")
			}
		}
		done <- err
	}()
	select {
	case <-ctx.Done():
		return nil, errors.New("query_failed")
	case err := <-done:
		if err != nil {
			return nil, errors.New("invalid_request")
		}
	}
	validPath := func(s string) bool {
		return filepath.IsAbs(s) && len(s) <= 4096 && strings.IndexFunc(s, unicode.IsControl) < 0
	}
	if q.V != 1 || !validPath(q.Executable) {
		return nil, errors.New("invalid_request")
	}
	var result map[string]any
	var err error
	if operation == "tmux.discovery" {
		if q.Namespace != "" || (q.Socket != "" && !validPath(q.Socket)) {
			return nil, errors.New("invalid_request")
		}
		result, err = q.tmuxDiscovery(ctx)
	} else {
		if q.Socket != "" || !herdrSession.MatchString(q.Namespace) || q.Namespace == "." || q.Namespace == ".." {
			return nil, errors.New("invalid_request")
		}
		result, err = q.herdrDiscovery(ctx, operation)
	}
	if err != nil {
		return nil, err
	}
	result["operation"], result["v"] = operation, 1
	data, err := json.Marshal(result)
	if err != nil || len(data)+1 > maxDiscoveryOutput {
		return nil, errors.New("output_limit")
	}
	return result, nil
}

var noTmuxServer = errors.New("no_tmux_server")

func (q workspaceQuery) run(ctx context.Context, args ...string) ([]byte, error) {
	if q.Namespace != "" {
		args = append([]string{"--session", q.Namespace}, args...)
	} else if q.Socket != "" {
		args = append([]string{"-S", q.Socket}, args...)
	}
	cmd := exec.CommandContext(ctx, q.Executable, args...)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		return key == "LC_ALL" || q.Namespace != "" && slices.Contains([]string{"HERDR_SESSION", "HERDR_SOCKET_PATH", "HERDR_CLIENT_SOCKET_PATH", "HERDR_PANE_ID", "HERDR_ENV"}, key)
	})
	cmd.Env = append(cmd.Env, "LC_ALL=C")
	out := &limitBuffer{limit: maxDiscoveryOutput}
	diag := &limitBuffer{limit: 16 * 1024}
	cmd.Stdout, cmd.Stderr = out, diag
	cmd.WaitDelay = 500 * time.Millisecond
	if err := cmd.Run(); err != nil {
		switch {
		case errors.Is(err, os.ErrPermission):
			return nil, errors.New("permission_denied")
		case errors.Is(err, os.ErrNotExist):
			return nil, errors.New("not_installed")
		case err.Error() == "command output limit":
			return nil, errors.New("output_limit")
		}
		message := diag.String()
		if strings.Contains(strings.ToLower(message), "permission denied") {
			return nil, errors.New("permission_denied")
		}
		if q.Namespace == "" && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 1 &&
			(strings.Contains(message, "no server running on ") || strings.Contains(message, "error connecting to ") && strings.Contains(message, "No such file or directory")) {
			return nil, noTmuxServer
		}
		return nil, errors.New("query_failed")
	}
	if !utf8.Valid(out.Bytes()) {
		return nil, errors.New("invalid_response")
	}
	return out.Bytes(), nil
}

type discoveryPane struct {
	SessionID      string `json:"session_id"`
	SessionName    string `json:"session_name"`
	WindowID       string `json:"window_id"`
	WindowName     string `json:"window_name"`
	WindowIndex    int    `json:"window_index"`
	WindowActive   bool   `json:"window_active"`
	PaneID         string `json:"pane_id"`
	PaneIndex      int    `json:"pane_index"`
	PaneActive     bool   `json:"pane_active"`
	HistorySize    int    `json:"history_size"`
	ServerIdentity string `json:"server_identity"`
}

func (q workspaceQuery) tmuxDiscovery(ctx context.Context) (map[string]any, error) {
	versionBytes, err := q.run(ctx, "-V")
	if err != nil {
		return nil, err
	}
	banner := strings.TrimSpace(string(versionBytes))
	token, ok := strings.CutPrefix(banner, "tmux ")
	if !ok {
		return nil, errors.New("target_version_unknown")
	}
	if err := checkTmuxVersion(token); err != nil {
		return nil, errors.New(targetErrorCode(err))
	}
	const format = "#{session_id}\t#{session_name}\t#{window_id}\t#{window_name}\t#{window_index}\t#{window_active}\t#{pane_id}\t#{pane_index}\t#{pane_active}\t#{history_size}\t#{pid}:#{start_time}:#{session_created}\t#{version}\t#{socket_path}"
	data, err := q.run(ctx, "-N", "-u", "list-panes", "-a", "-F", format)
	if errors.Is(err, noTmuxServer) {
		return map[string]any{"version": banner, "panes": []discoveryPane{}}, nil
	}
	if err != nil {
		return nil, err
	}
	panes, err := q.parseTmuxDirectory(data)
	if err != nil {
		return nil, err
	}
	return map[string]any{"version": banner, "panes": panes}, nil
}

func (q workspaceQuery) parseTmuxDirectory(data []byte) ([]discoveryPane, error) {
	invalid := errors.New("invalid_response")
	panes := []discoveryPane{}
	counts := map[string]int{}
	identities := map[string]string{}
	var instance string
	for raw := range bytes.Lines(data) {
		f := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\t")
		if len(f) != 13 || !tmuxSession.MatchString(f[0]) || !tmuxWindow.MatchString(f[2]) || !tmuxPane.MatchString(f[6]) ||
			(f[5] != "0" && f[5] != "1") || (f[8] != "0" && f[8] != "1") || !filepath.IsAbs(f[12]) {
			return nil, invalid
		}
		if err := checkTmuxVersion(f[11]); err != nil {
			return nil, errors.New(targetErrorCode(err))
		}
		if q.Socket != "" && filepath.Clean(q.Socket) != filepath.Clean(f[12]) {
			// tmux may canonicalize a macOS /var -> /private/var prefix.
			requested, e1 := os.Stat(q.Socket)
			actual, e2 := os.Stat(f[12])
			if e1 != nil || e2 != nil || !os.SameFile(requested, actual) {
				return nil, errors.New("server_changed")
			}
		}
		parts := strings.Split(f[10], ":")
		wi, e1 := strconv.Atoi(f[4])
		pi, e2 := strconv.Atoi(f[7])
		history, e3 := strconv.Atoi(f[9])
		if len(parts) != 3 || !intText(parts[0]) || !intText(parts[1]) || !intText(parts[2]) || e1 != nil || e2 != nil || e3 != nil || wi < 0 || pi < 0 || history < 0 {
			return nil, invalid
		}
		current := strings.Join(parts[:2], ":")
		if instance != "" && instance != current || identities[f[0]] != "" && identities[f[0]] != f[10] {
			return nil, errors.New("server_changed")
		}
		instance, identities[f[0]] = current, f[10]
		counts[f[0]]++
		if len(counts) > maxDiscoverySessions || counts[f[0]] > maxPanesPerSession {
			return nil, errors.New("output_limit")
		}
		panes = append(panes, discoveryPane{f[0], f[1], f[2], f[3], wi, f[5] == "1", f[6], pi, f[8] == "1", history, f[10]})
	}
	if len(panes) == 0 {
		return nil, invalid
	}
	return panes, nil
}

type directorySession struct {
	Name    string `json:"name"`
	Running *bool  `json:"running"`
}
type directoryStatus struct {
	Running            *bool  `json:"running"`
	Version            string `json:"version,omitempty"`
	Protocol           *int   `json:"protocol,omitempty"`
	Compatible         *bool  `json:"compatible,omitempty"`
	EndpointCompatible *bool  `json:"endpoint_compatible,omitempty"`
	Session            string `json:"session,omitempty"`
}

func (q workspaceQuery) herdrDiscovery(ctx context.Context, operation string) (map[string]any, error) {
	b, err := q.run(ctx, "--version")
	if err != nil {
		return nil, err
	}
	banner := strings.TrimSpace(string(b))
	v, ok := strings.CutPrefix(banner, "herdr ")
	if !ok {
		return nil, errors.New("target_version_unknown")
	}
	if err := checkHerdrVersion(v); err != nil {
		return nil, errors.New(targetErrorCode(err))
	}
	result := map[string]any{"version": banner}
	if operation == "herdr.snapshot" {
		path, err := herdrSocket(q.Namespace)
		if err != nil {
			return nil, errors.New("query_failed")
		}
		before, err := socketIdentity(path)
		if err != nil {
			return nil, errors.New("query_failed")
		}
		b, err = q.run(ctx, "api", "snapshot")
		if err != nil {
			return nil, err
		}
		after, err := socketIdentity(path)
		if err != nil || after != before {
			return nil, errors.New("server_changed")
		}
		var env struct {
			Result struct {
				Type     string
				Snapshot json.RawMessage
			}
		}
		var snapshot herdrSnapshot
		if json.Unmarshal(b, &env) != nil || env.Result.Type != "session_snapshot" || json.Unmarshal(env.Result.Snapshot, &snapshot) != nil || snapshot.Protocol == nil || snapshot.Panes == nil {
			return nil, errors.New("invalid_response")
		}
		if err := checkHerdrVersion(snapshot.Version); err != nil {
			return nil, errors.New(targetErrorCode(err))
		}
		result["snapshot"], result["server_identity"] = env.Result.Snapshot, before
		return result, nil
	}
	if operation == "herdr.probe" {
		b, err = q.run(ctx, "status", "server", "--json")
		if err != nil {
			return nil, err
		}
		var status directoryStatus
		if json.Unmarshal(b, &status) != nil || status.Running == nil || status.Session != q.Namespace && !(status.Session == "" && q.Namespace == "default") {
			return nil, errors.New("invalid_response")
		}
		if *status.Running {
			if status.Protocol == nil || *status.Protocol < 0 || status.Compatible == nil || status.EndpointCompatible == nil {
				return nil, errors.New("invalid_response")
			}
			if err := checkHerdrVersion(status.Version); err != nil {
				return nil, errors.New(targetErrorCode(err))
			}
			if !*status.Compatible || !*status.EndpointCompatible {
				return nil, errors.New("incompatible")
			}
		}
		result["status"] = status
	}
	b, err = q.run(ctx, "session", "list", "--json")
	if err != nil {
		return nil, err
	}
	var list struct{ Sessions []directorySession }
	if json.Unmarshal(b, &list) != nil || list.Sessions == nil {
		return nil, errors.New("invalid_response")
	}
	seen := map[string]bool{}
	for _, s := range list.Sessions {
		if s.Running == nil || !herdrSession.MatchString(s.Name) || s.Name == "." || s.Name == ".." || seen[s.Name] {
			return nil, errors.New("invalid_response")
		}
		seen[s.Name] = true
	}
	result["sessions"] = list.Sessions
	return result, nil
}
