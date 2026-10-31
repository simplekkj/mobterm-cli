package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"

	"github.com/pelletier/go-toml/v2"
)

var errCodexMixedHooks = errors.New("user hooks exist in both hooks.json and config.toml; choose one representation; configuration unchanged")

// Raw messages preserve unrelated values, including integers too large for a
// float64. The existing position parser also rejects ambiguous duplicate keys.
func parseCodexJSON(data []byte) (map[string]json.RawMessage, map[string][]json.RawMessage, error) {
	root := map[string]json.RawMessage{}
	hooks := map[string][]json.RawMessage{}
	if data == nil {
		return root, hooks, nil
	}
	if !json.Valid(data) {
		return nil, nil, errors.New("invalid Codex hooks JSON")
	}
	if _, err := parseJSONC(data); err != nil {
		return nil, nil, err
	}
	if json.Unmarshal(data, &root) != nil || root == nil {
		return nil, nil, errors.New("invalid Codex hooks object")
	}
	if raw, ok := root["hooks"]; ok {
		if json.Unmarshal(raw, &hooks) != nil || hooks == nil {
			return nil, nil, errors.New("invalid Codex JSON events")
		}
		for _, groups := range hooks {
			if groups == nil {
				return nil, nil, errors.New("invalid Codex JSON matcher groups")
			}
			for _, group := range groups {
				var entry map[string]json.RawMessage
				if json.Unmarshal(group, &entry) != nil || entry == nil {
					return nil, nil, errors.New("invalid Codex JSON matcher group")
				}
			}
		}
	}
	return root, hooks, nil
}

func codexJSONBlock(executable string) string {
	var parsed map[string]any
	_ = toml.Unmarshal([]byte(codexNativeBlock(executable)), &parsed)
	encoded, _ := json.Marshal(parsed["hooks"])
	return string(encoded)
}

func equalHookJSON(a, b []byte) bool {
	var left, right any
	for i, data := range [][]byte{a, b} {
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		v := &left
		if i == 1 {
			v = &right
		}
		if d.Decode(v) != nil {
			return false
		}
	}
	return reflect.DeepEqual(left, right)
}

func encodeCodexJSON(root map[string]json.RawMessage, hooks map[string][]json.RawMessage) []byte {
	root["hooks"], _ = json.Marshal(hooks)
	data, _ := json.MarshalIndent(root, "", "  ")
	return append(data, '\n')
}

func stripCodexJSON(data []byte, owned string) ([]byte, error) {
	root, hooks, err := parseCodexJSON(data)
	if err != nil || owned == "" {
		return data, err
	}
	var expected map[string][]json.RawMessage
	if json.Unmarshal([]byte(owned), &expected) != nil || len(expected) == 0 {
		return nil, errors.New("invalid Codex JSON ownership")
	}
	for event, groups := range expected {
		if len(groups) != 1 {
			return nil, errors.New("invalid owned Codex JSON group")
		}
		kept := []json.RawMessage{}
		count := 0
		for _, group := range hooks[event] {
			if equalHookJSON(group, groups[0]) {
				count++
			} else {
				kept = append(kept, group)
			}
		}
		if count != 1 {
			return nil, errors.New("managed Codex JSON hook edited, missing or duplicated; unchanged")
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	return encodeCodexJSON(root, hooks), nil
}

// Trust state is stored under hooks.state even for JSON definitions. It is not
// an inline event source and must never be moved or granted by the installer.
func codexTOMLEvents(data []byte) (bool, error) {
	var root map[string]any
	if toml.Unmarshal(data, &root) != nil {
		return false, errors.New("invalid Codex TOML")
	}
	raw, present := root["hooks"]
	if !present {
		return false, nil
	}
	hooks, ok := raw.(map[string]any)
	if !ok {
		return false, errors.New("invalid Codex TOML hooks table")
	}
	hasEvents := false
	for event, value := range hooks {
		if event == "state" {
			continue
		}
		groups, ok := value.([]any)
		if !ok {
			return false, errors.New("invalid Codex TOML matcher groups")
		}
		hasEvents = hasEvents || len(groups) > 0
	}
	return hasEvents, nil
}

func selectCodexHookLayout(cleanTOML, cleanJSON []byte, previous HookRecord) (string, error) {
	hasTOML, err := codexTOMLEvents(cleanTOML)
	if err != nil {
		return "", err
	}
	_, hooks, err := parseCodexJSON(cleanJSON)
	if err != nil {
		return "", err
	}
	hasJSON := false
	for _, entries := range hooks {
		hasJSON = hasJSON || len(entries) > 0
	}
	if hasTOML && hasJSON {
		return "", errCodexMixedHooks
	}
	if hasJSON {
		return "json", nil
	}
	if hasTOML {
		return "toml", nil
	}
	if previous.CodexNative != "" {
		return "toml", nil
	}
	return "json", nil
}

func inspectCodexLayout(data []byte, r HookRecord, executable string) (string, error) {
	if r.CodexNative != "" && (r.CodexJSON != "" || r.CodexNative != codexNativeBlock(executable)) || r.CodexJSON != "" && r.CodexJSON != codexJSONBlock(executable) {
		return "", errors.New("invalid Codex hook ownership")
	}
	cleanTOML, err := stripCodexNative(data, r.CodexNative)
	if err != nil {
		return "", err
	}
	jsonData, _, err := readHookFile(filepath.Join(filepath.Dir(r.Path), "hooks.json"))
	if err != nil {
		return "", err
	}
	cleanJSON, err := stripCodexJSON(jsonData, r.CodexJSON)
	if err != nil {
		return "", err
	}
	return selectCodexHookLayout(cleanTOML, cleanJSON, r)
}

func editCodexLayout(data []byte, action, path, executable string, r HookRecord, installNative bool) ([]byte, HookRecord, *hookFileChange, error) {
	// Keep legacy notify-only installs independent of unrelated JSON hooks.
	if action == "uninstall" && r.CodexJSON == "" || !installNative && r.CodexNative == "" && r.CodexJSON == "" {
		result, record, err := editCodexWithNative(data, action, path, executable, r, false)
		return result, record, nil, err
	}
	original := r
	if r.CodexNative != "" && r.CodexJSON != "" {
		return nil, r, nil, errors.New("ambiguous Codex hook ownership")
	}
	if r.CodexNative == "" && bytes.Contains(data, []byte("# mobterm-cli managed native hooks")) {
		return nil, r, nil, errors.New("Codex native hook ownership missing; unchanged")
	}
	jsonData, file, err := readHookFile(filepath.Join(filepath.Dir(path), "hooks.json"))
	if err != nil {
		return nil, r, nil, err
	}
	cleanTOML, err := stripCodexNative(data, r.CodexNative)
	if err != nil {
		return nil, r, nil, err
	}
	cleanJSON, err := stripCodexJSON(jsonData, r.CodexJSON)
	if err != nil {
		return nil, r, nil, err
	}
	layout := ""
	if action == "install" {
		layout, err = selectCodexHookLayout(cleanTOML, cleanJSON, r)
		if err != nil {
			return nil, r, nil, err
		}
		var parsedTOML, parsedJSON map[string]any
		_ = toml.Unmarshal(cleanTOML, &parsedTOML)
		_ = json.Unmarshal(cleanJSON, &parsedJSON)
		if containsCodexNativeCommand(parsedTOML, executable) || containsCodexNativeCommand(parsedJSON, executable) {
			return nil, r, nil, errors.New("existing Codex native commands have unknown ownership")
		}
	}
	r.CodexNative, r.CodexJSON = "", ""
	result, record, err := editCodexWithNative(cleanTOML, action, path, executable, r, layout == "toml")
	if err != nil {
		return nil, original, nil, err
	}
	if layout == "json" {
		root, hooks, err := parseCodexJSON(cleanJSON)
		if err != nil {
			return nil, original, nil, err
		}
		record.CodexJSON = codexJSONBlock(executable)
		var owned map[string][]json.RawMessage
		_ = json.Unmarshal([]byte(record.CodexJSON), &owned)
		for event, entries := range owned {
			hooks[event] = append(hooks[event], entries...)
		}
		cleanJSON = encodeCodexJSON(root, hooks)
	}
	// Repeated installs preserve the existing bytes, mode and modification time.
	if equalHookJSON(jsonData, cleanJSON) {
		cleanJSON = jsonData
	}
	file.After, file.AfterSHA = cleanJSON, digest(string(cleanJSON))
	file.AfterExists = file.BeforeExists || cleanJSON != nil
	if len(cleanJSON) > 2*1024*1024 {
		return nil, original, nil, errors.New("Codex JSON hooks exceed size limit")
	}
	return result, record, &file, nil
}
