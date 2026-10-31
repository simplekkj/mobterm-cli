package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// The private journal stores bounded replacements and hashes, not an
// unbounded history. No discovery operation writes or resumes this journal.
type hookTransaction struct {
	V               int             `json:"v"`
	Agent           string          `json:"agent"`
	Path            string          `json:"path"`
	Action          string          `json:"action"`
	BeforeSHA       string          `json:"before_sha"`
	BeforeExists    bool            `json:"before_exists"`
	After           []byte          `json:"after"`
	AfterSHA        string          `json:"after_sha"`
	Mode            uint32          `json:"mode"`
	BeforeRecord    *HookRecord     `json:"before_record,omitempty"`
	AfterRecord     *HookRecord     `json:"after_record,omitempty"`
	BeforeRecordSHA string          `json:"before_record_sha"`
	AfterRecordSHA  string          `json:"after_record_sha"`
	JSONFile        *hookFileChange `json:"json_file,omitempty"`
	JSONFirst       bool            `json:"json_first,omitempty"`
}

// v2 journals also guard the adjacent hooks.json, including absence. Each file
// is atomically replaced; the journal makes interrupted multi-file work resumable.
type hookFileChange struct {
	Path         string `json:"path"`
	BeforeSHA    string `json:"before_sha"`
	BeforeExists bool   `json:"before_exists"`
	After        []byte `json:"after"`
	AfterSHA     string `json:"after_sha"`
	AfterExists  bool   `json:"after_exists"`
	Mode         uint32 `json:"mode"`
}

func (f hookFileChange) changed() bool {
	return f.BeforeExists != f.AfterExists || f.BeforeSHA != f.AfterSHA
}

func readHookFile(path string) ([]byte, hookFileChange, error) {
	if err := nativeRealParents(path); err != nil {
		return nil, hookFileChange{}, err
	}
	data, mode, err := secureReadWithMode(path, 2*1024*1024)
	exists := !errors.Is(err, os.ErrNotExist)
	if err != nil && exists {
		return nil, hookFileChange{}, err
	}
	if !exists {
		mode = 0600
	}
	return data, hookFileChange{Path: path, BeforeSHA: digest(string(data)), BeforeExists: exists, After: data, AfterSHA: digest(string(data)), AfterExists: exists, Mode: uint32(mode)}, nil
}

func (f hookFileChange) state() (bool, error) {
	data, current, err := readHookFile(f.Path)
	if err != nil {
		return false, err
	}
	after := current.BeforeExists == f.AfterExists && digest(string(data)) == f.AfterSHA
	before := current.BeforeExists == f.BeforeExists && current.BeforeSHA == f.BeforeSHA
	if !before && !after || current.BeforeExists && !after && current.Mode != f.Mode {
		return false, errors.New("hook recovery conflicts with JSON configuration; unchanged")
	}
	return after, nil
}

func (f hookFileChange) finish() error {
	after, err := f.state()
	if err != nil || after {
		return err
	}
	if !f.AfterExists {
		return removeHookFile(f.Path)
	}
	return atomicWrite(f.Path, f.After, os.FileMode(f.Mode))
}

func hookTransactionName(agent string) string { return "hooks-" + agent + "-pending.json" }
func readHookTransaction(s Store, agent string) (hookTransaction, error) {
	var tx hookTransaction
	b, err := secureRead(filepath.Join(s.Dir, hookTransactionName(agent)), 8*1024*1024)
	if err != nil {
		return tx, err
	}
	if json.Unmarshal(b, &tx) != nil || (tx.V != 1 && tx.V != 2) || tx.Agent != agent || (tx.Action != "install" && tx.Action != "uninstall") || !filepath.IsAbs(tx.Path) || len(tx.After) > 2*1024*1024 || len(tx.BeforeSHA) != 64 || tx.AfterSHA != digest(string(tx.After)) || tx.BeforeRecordSHA != hookRecordDigest(tx.BeforeRecord) || tx.AfterRecordSHA != hookRecordDigest(tx.AfterRecord) || tx.Mode&^0777 != 0 || tx.Action == "install" && tx.AfterRecord == nil || tx.Action == "uninstall" && tx.AfterRecord != nil {
		return tx, errors.New("hook recovery journal invalid; configuration unchanged")
	}
	if tx.V == 1 && (tx.JSONFile != nil || tx.JSONFirst) || tx.V == 2 && (agent != "codex" || tx.JSONFile == nil) {
		return tx, errors.New("hook recovery layout invalid")
	}
	if f := tx.JSONFile; f != nil {
		if f.Path != filepath.Join(filepath.Dir(tx.Path), "hooks.json") || len(f.BeforeSHA) != 64 || f.AfterSHA != digest(string(f.After)) || len(f.After) > 2*1024*1024 || f.Mode&^0777 != 0 || !f.AfterExists && len(f.After) != 0 {
			return tx, errors.New("hook recovery JSON journal invalid")
		}
	}
	for _, r := range []*HookRecord{tx.BeforeRecord, tx.AfterRecord} {
		if r != nil && (r.Agent != agent || r.Path != tx.Path || r.Managed == "" || r.CodexJSON != "" && tx.JSONFile == nil) {
			return tx, errors.New("hook recovery ownership invalid")
		}
	}
	return tx, nil
}
func hookTransactionState(s Store, tx hookTransaction) ([]byte, bool, error) {
	if tx.JSONFile != nil {
		if _, err := tx.JSONFile.state(); err != nil {
			return nil, false, err
		}
	}
	if err := nativeRealParents(tx.Path); err != nil {
		return nil, false, err
	}
	data, mode, err := secureReadWithMode(tx.Path, 2*1024*1024)
	exists := !errors.Is(err, os.ErrNotExist)
	if err != nil && exists {
		return nil, false, err
	}
	before := exists == tx.BeforeExists && digest(string(data)) == tx.BeforeSHA
	after := exists && digest(string(data)) == tx.AfterSHA
	if !before && !after {
		return nil, false, errors.New("hook recovery conflicts with user configuration; unchanged")
	}
	// v1 journals already record the original permissions. A before-state
	// replacement must still match them, including at the pre-write recheck.
	// After-state recovery only finishes ownership and never rewrites config.
	if exists && !after && mode != os.FileMode(tx.Mode) {
		return nil, false, errors.New("hook recovery conflicts with configuration permissions; unchanged")
	}
	var r HookRecord
	err = s.readJSON("hooks-"+tx.Agent+".json", &r)
	var current *HookRecord
	if err == nil {
		current = &r
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	if hookRecordDigest(current) != tx.BeforeRecordSHA && hookRecordDigest(current) != tx.AfterRecordSHA {
		return nil, false, errors.New("hook recovery conflicts with ownership; unchanged")
	}
	return data, after, nil
}
func removeHookFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func finishHookTransaction(s Store, tx hookTransaction) error {
	data, after, err := hookTransactionState(s, tx)
	if err != nil {
		return err
	}
	if tx.Action == "install" {
		if err = s.writeJSON("hooks-"+tx.Agent+".json", tx.AfterRecord); err != nil {
			return err
		}
	}
	// Recheck after ownership persistence, immediately before replacing config.
	current, _, err := hookTransactionState(s, tx)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, data) {
		return errors.New("hook config changed concurrently; retry")
	}
	if tx.JSONFile != nil && tx.JSONFirst {
		if err = tx.JSONFile.finish(); err != nil {
			return err
		}
		if _, after, err = hookTransactionState(s, tx); err != nil {
			return err
		}
	}
	if !after {
		if err = atomicWrite(tx.Path, tx.After, os.FileMode(tx.Mode)); err != nil {
			return err
		}
	}
	if tx.JSONFile != nil && !tx.JSONFirst {
		if _, _, err = hookTransactionState(s, tx); err != nil {
			return err
		}
		if err = tx.JSONFile.finish(); err != nil {
			return err
		}
	}
	if tx.Action == "uninstall" {
		if err = removeHookFile(filepath.Join(s.Dir, "hooks-"+tx.Agent+".json")); err != nil {
			return err
		}
	}
	return removeHookFile(filepath.Join(s.Dir, hookTransactionName(tx.Agent)))
}

// Every persisted value must fit its normal reader. Large original notify argv
// can otherwise duplicate into Original and Chain and strand an unreadable record.
func hookRecordDigest(record *HookRecord) string {
	data, _ := json.Marshal(record)
	return digest(string(data))
}
func writeHookTransaction(s Store, tx hookTransaction) error {
	tx.BeforeRecordSHA = hookRecordDigest(tx.BeforeRecord)
	tx.AfterRecordSHA = hookRecordDigest(tx.AfterRecord)
	if tx.AfterRecord != nil {
		record, err := json.MarshalIndent(tx.AfterRecord, "", "  ")
		if err != nil {
			return err
		}
		if len(record)+1 > 2*1024*1024 {
			return errors.New("hook ownership exceeds size limit")
		}
	}
	data, err := json.MarshalIndent(tx, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > 8*1024*1024 {
		return errors.New("hook recovery journal exceeds size limit")
	}
	return atomicWrite(filepath.Join(s.Dir, hookTransactionName(tx.Agent)), append(data, '\n'), 0600)
}
