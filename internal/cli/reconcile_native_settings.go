package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	errAdapterDisabled = errors.New("adapter disabled by agent settings")
	errAdapterSettings = errors.New("cannot verify agent adapter settings")
)

func claudeHooksDisabled(root *jsoncNode) bool {
	for _, key := range []string{"disableAllHooks", "allowManagedHooksOnly"} {
		if setting := root.keys[key]; setting != nil && setting.kind != 'f' {
			return true
		}
	}
	return false
}

// Claude's personal settings govern plugin hooks as well as classic hooks.
// Pi applies personal extension overrides relative to its selected agent dir.
func checkNativeSettings(ctx context.Context, agent, assetPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var settingsPath string
	switch agent {
	case "claude":
		settingsPath = assetPath
	case "pi":
		settingsPath = filepath.Join(filepath.Dir(filepath.Dir(assetPath)), "settings.json")
	default:
		return nil
	}
	if nativeMigrationNode(settingsPath, false) != nil {
		return errAdapterSettings
	}
	data, exists, _, err := nativeRead(settingsPath)
	if err != nil {
		return errAdapterSettings
	}
	if !exists {
		return nil
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if !json.Valid(data) {
		return errAdapterSettings
	}
	root, err := parseJSONC(data) // also rejects duplicate keys
	if err != nil {
		return errAdapterSettings
	}
	if agent == "claude" {
		if claudeHooksDisabled(root) {
			return errAdapterDisabled
		}
		return nil
	}
	settings := root.keys["extensions"]
	if settings == nil || settings.kind == 'n' {
		return nil
	}
	if settings.kind != '[' || len(settings.children) > 1024 {
		return errAdapterSettings
	}
	rel, err := filepath.Rel(filepath.Dir(settingsPath), assetPath)
	if err != nil {
		return errAdapterSettings
	}
	var excluded, forceInclude, forceExclude bool
	for _, entry := range settings.children {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.kind != '"' {
			return errAdapterSettings
		}
		rule := entry.text
		if rule == "" || !strings.ContainsAny(rule[:1], "!+-") {
			continue
		}
		op, pattern := rule[0], rule[1:]
		if op != '!' && (strings.HasPrefix(pattern, "./") || strings.HasPrefix(pattern, `.\`)) {
			pattern = pattern[2:]
		}
		if strings.HasPrefix(pattern, "file://") || len(pattern) > 4096 {
			return errAdapterSettings
		}
		if strings.HasPrefix(pattern, "~/") || pattern == "~" {
			home, e := os.UserHomeDir()
			if e != nil {
				return errAdapterSettings
			}
			suffix := strings.TrimPrefix(strings.TrimPrefix(pattern, "~"), "/")
			pattern = filepath.Join(home, suffix)
			if strings.HasSuffix(suffix, "/") && pattern != "/" {
				pattern += "/"
			}
		}
		switch op {
		case '-':
			forceExclude = forceExclude || pattern == rel || pattern == assetPath
		case '+':
			forceInclude = forceInclude || pattern == rel || pattern == assetPath
		case '!':
			for _, candidate := range []string{rel, filepath.Base(assetPath), assetPath} {
				match, e := piExclusionMatches(ctx, pattern, candidate)
				if e != nil {
					return e
				}
				if match {
					excluded = true
					break
				}
			}
		}
	}
	if forceExclude || excluded && !forceInclude {
		return errAdapterDisabled
	}
	return nil
}

// Minimatch-style path segments for ordinary literals, *, ?, and globstar.
// Refuse patterns requiring brace, extglob, character class, escape or path
// normalization semantics rather than treating a possible exclusion as absent.
func piExclusionMatches(ctx context.Context, pattern, candidate string) (bool, error) {
	if strings.ContainsAny(pattern, `[]{}()\`) || strings.HasPrefix(pattern, "!") || strings.Contains(pattern, "//") ||
		strings.Contains("/"+pattern+"/", "/../") || strings.Contains("/"+pattern+"/", "/./") {
		return false, errAdapterSettings
	}
	if strings.HasPrefix(pattern, "#") || pattern == "" {
		return false, nil
	}
	if strings.Contains(pattern, "?") {
		for _, c := range candidate {
			if c > 127 {
				return false, errAdapterSettings
			}
		}
	}
	if strings.HasSuffix(pattern, "/") {
		return false, nil
	}
	// A relative globstar can match an absolute path in minimatch. The
	// absolute path's initial empty segment is part of that comparison.
	parts := strings.Split(candidate, "/")
	reachable := make([]bool, len(parts)+1)
	reachable[0] = true
	segments := strings.Split(pattern, "/")
	if len(segments) > 128 || len(parts) > 128 {
		return false, errAdapterSettings
	}
	for j, glob := range segments {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if glob == "**" {
			// A terminal "/**" denotes descendants, not the prefix itself.
			if j == len(segments)-1 && j != 0 {
				for i := len(parts); i > 0; i-- {
					reachable[i] = reachable[i-1] && !strings.HasPrefix(parts[i-1], ".")
				}
				reachable[0] = false
			}
			for i, part := range parts {
				if !strings.HasPrefix(part, ".") {
					reachable[i+1] = reachable[i+1] || reachable[i]
				}
			}
			continue
		}
		if strings.Contains(glob, "**") {
			return false, errAdapterSettings
		}
		for i := len(parts) - 1; i >= 0; i-- {
			match, _ := path.Match(glob, parts[i])
			reachable[i+1] = reachable[i] && match && (parts[i] != "" || glob == "") &&
				(!strings.HasPrefix(parts[i], ".") || strings.HasPrefix(glob, "."))
		}
		reachable[0] = false
	}
	return reachable[len(parts)], nil
}
