package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed adapters/opencode/package.json adapters/opencode/tui.js adapters/pi/mobterm-jumpto.ts
var nativeAssets embed.FS

const nativeAdapterRevision = 1

type nativeOwnedFile struct {
	SHA         string `json:"sha256"`
	PreviousSHA string `json:"previous_sha256,omitempty"`
}
type nativeHookRecord struct {
	V                 int                        `json:"v"`
	Agent             string                     `json:"agent"`
	Path              string                     `json:"path"`
	Reference         string                     `json:"reference,omitempty"`
	Executable        string                     `json:"executable"`
	Store             string                     `json:"store"`
	Revision          int                        `json:"revision"`
	Pending           string                     `json:"pending,omitempty"`
	ReferenceRequired bool                       `json:"reference_required,omitempty"`
	Files             map[string]nativeOwnedFile `json:"files"`
}

func nativeDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func nativeConfigPath(agent string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	var root string
	switch agent {
	case "opencode":
		root = os.Getenv("OPENCODE_CONFIG_DIR")
		if root == "" {
			root = os.Getenv("XDG_CONFIG_HOME")
			if root == "" {
				root = filepath.Join(home, ".config")
			}
			root = filepath.Join(root, "opencode")
		}
		root = filepath.Join(root, "cli.json")
	case "pi":
		root = os.Getenv("PI_CODING_AGENT_DIR")
		if root == "" {
			root = filepath.Join(home, ".pi", "agent")
		}
		if root == "~" {
			root = home
		} else if strings.HasPrefix(root, "~/") {
			root = filepath.Join(home, root[2:])
		}
		root = filepath.Join(root, "extensions", "mobterm-jumpto.ts")
	default:
		return "", errors.New("unknown native agent")
	}
	return filepath.Abs(root)
}

// Verify every existing directory, including ancestors of the destination. The
// two fixed macOS system aliases are not user configuration links.
func nativeRealParents(path string) error {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err == nil {
			systemAlias := runtime.GOOS == "darwin" && (dir == "/var" || dir == "/tmp")
			if systemAlias && info.Mode()&os.ModeSymlink != 0 {
				target, e := os.Readlink(dir)
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(dir), target)
				}
				if e != nil || target != "/private"+dir {
					return errors.New("unsafe system directory alias")
				}
			} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("native integration directory is not a real directory")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if filepath.Dir(dir) == dir {
			return nil
		}
	}
}
func nativeRead(path string) ([]byte, bool, os.FileMode, error) {
	if err := nativeRealParents(path); err != nil {
		return nil, false, 0, err
	}
	data, err := secureRead(path, 2*1024*1024)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, 0600, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, false, 0, err
	}
	return data, true, info.Mode().Perm(), nil
}
func nativeRenderedFiles(s Store, agent, path, executable string) (map[string][]byte, string, error) {
	cli, _ := json.Marshal(executable)
	state, _ := json.Marshal(s.Dir)
	render := func(name string) ([]byte, error) {
		b, err := nativeAssets.ReadFile(name)
		if err != nil {
			return nil, err
		}
		b = bytes.ReplaceAll(b, []byte("__MOBTERM_CLI_JSON__"), cli)
		b = bytes.ReplaceAll(b, []byte("__MOBTERM_STORE_JSON__"), state)
		return b, nil
	}
	files := map[string][]byte{}
	reference := ""
	if agent == "pi" {
		b, err := render("adapters/pi/mobterm-jumpto.ts")
		if err != nil {
			return nil, "", err
		}
		files[path] = b
	} else {
		reference = filepath.Join(s.Dir, "native", "opencode")
		for _, name := range []string{"package.json", "tui.js"} {
			b, err := render("adapters/opencode/" + name)
			if err != nil {
				return nil, "", err
			}
			files[filepath.Join(reference, name)] = b
		}
	}
	return files, reference, nil
}
func nativeRecordName(agent string) string { return "native-hooks-" + agent + ".json" }

func nativeOtherOpenCodeInstallation(data []byte, reference string) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	root, err := parseJSONC(data)
	if err != nil {
		return err
	}
	plugins := root.keys["plugins"]
	if plugins == nil || plugins.kind != '[' {
		return nil
	}
	for _, entry := range plugins.children {
		if entry.kind == '{' {
			entry = entry.keys["package"]
		}
		if entry == nil || entry.kind != '"' || entry.text == reference || !filepath.IsAbs(entry.text) {
			continue
		}
		data, exists, _, err := nativeRead(filepath.Join(entry.text, "package.json"))
		if err != nil || !exists {
			continue
		}
		var manifest struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &manifest) == nil && manifest.Name == "mobterm-jumpto" {
			return errors.New("OpenCode already references another Mobterm installation; uninstall it using its owning state directory first")
		}
	}
	return nil
}
func nativeValidateRecord(s Store, r nativeHookRecord, agent string) error {
	if r.V != 1 || r.Agent != agent || r.Store != s.Dir || !filepath.IsAbs(r.Path) || !filepath.IsAbs(r.Executable) || r.Revision < 1 || len(r.Files) == 0 {
		return errors.New("invalid native integration ownership record")
	}
	files, reference, err := nativeRenderedFiles(s, agent, r.Path, r.Executable)
	if err != nil {
		return err
	}
	if r.Reference != reference || len(files) != len(r.Files) {
		return errors.New("invalid native integration asset paths")
	}
	for path, file := range r.Files {
		if _, ok := files[path]; !ok || len(file.SHA) != 64 {
			return errors.New("invalid native integration asset ownership")
		}
	}
	if r.Pending != "" && r.Pending != "install" && r.Pending != "uninstall" {
		return errors.New("invalid native integration operation")
	}
	return nil
}
func nativeAllowed(data []byte, exists bool, file nativeOwnedFile, pending string) bool {
	if !exists {
		return pending == "uninstall" || pending == "install" && file.PreviousSHA == ""
	}
	digest := nativeDigest(data)
	return digest == file.SHA || (pending != "" && file.PreviousSHA != "" && digest == file.PreviousSHA)
}
func nativeHooksInstalled(s Store, agent string) bool {
	var r nativeHookRecord
	if s.readJSON(nativeRecordName(agent), &r) != nil || nativeValidateRecord(s, r, agent) != nil || r.Pending != "" {
		return false
	}
	path, err := nativeConfigPath(agent)
	if err != nil || path != r.Path {
		return false
	}
	for path, file := range r.Files {
		b, exists, _, err := nativeRead(path)
		if err != nil || !exists || nativeDigest(b) != file.SHA {
			return false
		}
	}
	if agent == "opencode" {
		b, exists, _, err := nativeRead(r.Path)
		if err != nil || !exists {
			return false
		}
		_, err = editNativePlugin(b, r.Reference, "install", true, false)
		if err != nil {
			return false
		}
	}
	return true
}
func changeNativeHooks(ctx context.Context, s Store, agent, action, path, executable string, checkVersion bool) error {
	if action != "install" && action != "uninstall" {
		return errors.New("use hooks install or uninstall")
	}
	if checkVersion && action == "install" {
		if err := hookVersions(ctx, agent); err != nil {
			return err
		}
	}
	if !filepath.IsAbs(s.Dir) {
		return errors.New("Mobterm state directory must be absolute")
	}
	if action == "install" && !filepath.IsAbs(executable) {
		return errors.New("native integration requires an absolute CLI executable")
	}
	if err := nativeRealParents(filepath.Join(s.Dir, "ownership")); err != nil {
		return err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	var r nativeHookRecord
	err = s.readJSON(nativeRecordName(agent), &r)
	owned := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if owned {
		if err = nativeValidateRecord(s, r, agent); err != nil {
			return err
		}
	}
	if action == "uninstall" && !owned {
		return nil
	}
	if path == "" {
		path, err = nativeConfigPath(agent)
	} else {
		path, err = filepath.Abs(path)
	}
	if err != nil {
		return err
	}
	if owned {
		if action == "install" && path != r.Path {
			return errors.New("native integration is managed at another configuration root; uninstall the recorded integration first")
		}
		// Uninstall always uses the recorded root, even after environment/root changes.
		if action == "uninstall" {
			path = r.Path
		}
	}
	if err = nativeRealParents(path); err != nil {
		return err
	}
	if err = ensureDir(filepath.Dir(path), false); err != nil {
		return err
	}
	configUnlock, err := lockFile(ctx, path+".mobterm-lock")
	if err != nil {
		return err
	}
	defer configUnlock()
	var config, result []byte
	mode := os.FileMode(0600)
	configExists := false
	if agent == "opencode" {
		config, configExists, mode, err = nativeRead(path)
		if err != nil {
			return err
		}
	}
	files, reference, err := nativeRenderedFiles(s, agent, path, executable)
	if err != nil {
		return err
	}
	if owned {
		reference = r.Reference
	}
	if agent == "opencode" {
		if action == "install" {
			if err = nativeOtherOpenCodeInstallation(config, reference); err != nil {
				return err
			}
		}
		allowMissing := r.Pending == "uninstall" || r.Pending == "install" && !r.ReferenceRequired
		result, err = editNativePlugin(config, reference, action, owned, allowMissing)
		if err != nil {
			return err
		}
	}
	// Inspect every owned asset before changing any file or reference.
	if owned {
		for asset, file := range r.Files {
			data, exists, _, e := nativeRead(asset)
			if e != nil {
				return e
			}
			if !nativeAllowed(data, exists, file, r.Pending) {
				return errors.New("managed native asset was edited or removed; integration unchanged")
			}
		}
	} else {
		for asset := range files {
			_, exists, _, e := nativeRead(asset)
			if e != nil {
				return e
			}
			if exists {
				return errors.New("native asset already exists without ownership; integration unchanged")
			}
		}
	}
	if action == "install" {
		if r.Pending == "uninstall" {
			return errors.New("native uninstall is incomplete; retry uninstall first")
		}
		next := nativeHookRecord{V: 1, Agent: agent, Path: path, Reference: reference, Executable: executable, Store: s.Dir, Revision: nativeAdapterRevision, Pending: "install", Files: map[string]nativeOwnedFile{}}
		next.ReferenceRequired = owned && (r.Pending == "" || r.ReferenceRequired)
		for asset, data := range files {
			next.Files[asset] = nativeOwnedFile{SHA: nativeDigest(data)}
			if owned {
				old := r.Files[asset]
				n := next.Files[asset]
				n.PreviousSHA = old.SHA
				if r.Pending == "install" {
					if old.SHA != n.SHA || r.Executable != executable {
						return errors.New("native installation incomplete; retry with the original CLI before upgrading")
					}
					n.PreviousSHA = old.PreviousSHA
				}
				next.Files[asset] = n
			}
			if err = nativeRealParents(asset); err != nil {
				return err
			}
			if err = ensureDir(filepath.Dir(asset), false); err != nil {
				return err
			}
		}
		// Persist intent before the first asset write. Interrupted installs are safely
		// retryable without adopting files that existed before this transaction.
		if err = s.writeJSON(nativeRecordName(agent), next); err != nil {
			return err
		}
		for asset, data := range files {
			current, exists, _, e := nativeRead(asset)
			if e != nil {
				return nativeIncomplete(e)
			}
			if !nativeAllowed(current, exists, next.Files[asset], "install") {
				return nativeIncomplete(errors.New("native asset changed concurrently"))
			}
			if !bytes.Equal(current, data) {
				if err = atomicWrite(asset, data, 0600); err != nil {
					return nativeIncomplete(err)
				}
			}
		}
		r = next
	} else {
		r.Pending = "uninstall"
		if err = s.writeJSON(nativeRecordName(agent), r); err != nil {
			return err
		}
	}
	if agent == "opencode" && !bytes.Equal(config, result) {
		current, exists, _, e := nativeRead(path)
		if e != nil {
			return nativeIncomplete(e)
		}
		if exists != configExists || !bytes.Equal(current, config) {
			return nativeIncomplete(errors.New("OpenCode config changed concurrently"))
		}
		if err = atomicWrite(path, result, mode); err != nil {
			return nativeIncomplete(err)
		}
	}
	if action == "uninstall" {
		for asset, file := range r.Files {
			current, exists, _, e := nativeRead(asset)
			if e != nil {
				return nativeIncomplete(e)
			}
			if !nativeAllowed(current, exists, file, r.Pending) {
				return nativeIncomplete(errors.New("native asset changed concurrently"))
			}
			if exists {
				if err = os.Remove(asset); err != nil {
					return nativeIncomplete(err)
				}
			}
		}
		if err = os.Remove(filepath.Join(s.Dir, nativeRecordName(agent))); err != nil {
			return nativeIncomplete(err)
		}
		if agent == "opencode" {
			_ = os.Remove(reference)
		} // Empty owned package directory only.
		return nil
	}
	r.Pending = ""
	for asset, file := range r.Files {
		file.PreviousSHA = ""
		r.Files[asset] = file
	}
	if err = s.writeJSON(nativeRecordName(agent), r); err != nil {
		return nativeIncomplete(err)
	}
	return nil
}
func nativeIncomplete(err error) error {
	return fmt.Errorf("native integration partially updated; ownership retained, retry the same operation: %w", err)
}
