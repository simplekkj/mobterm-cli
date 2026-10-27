package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// The private journal stores one bounded replacement and hashes, not an
// unbounded history. No discovery operation writes or resumes this journal.
type hookTransaction struct {
	V               int         `json:"v"`
	Agent           string      `json:"agent"`
	Path            string      `json:"path"`
	Action          string      `json:"action"`
	BeforeSHA       string      `json:"before_sha"`
	BeforeExists    bool        `json:"before_exists"`
	After           []byte      `json:"after"`
	AfterSHA        string      `json:"after_sha"`
	Mode            uint32      `json:"mode"`
	BeforeRecord    *HookRecord `json:"before_record,omitempty"`
	AfterRecord     *HookRecord `json:"after_record,omitempty"`
	BeforeRecordSHA string      `json:"before_record_sha"`
	AfterRecordSHA  string      `json:"after_record_sha"`
}

func hookTransactionName(agent string) string { return "hooks-" + agent + "-pending.json" }
func readHookTransaction(s Store, agent string) (hookTransaction, error) {
	var tx hookTransaction
	b, err := secureRead(filepath.Join(s.Dir, hookTransactionName(agent)), 8*1024*1024)
	if err != nil {
		return tx, err
	}
	if json.Unmarshal(b, &tx) != nil || tx.V != 1 || tx.Agent != agent || (tx.Action != "install" && tx.Action != "uninstall") || !filepath.IsAbs(tx.Path) || len(tx.After) > 2*1024*1024 || len(tx.BeforeSHA) != 64 || tx.AfterSHA != digest(string(tx.After)) || tx.BeforeRecordSHA != hookRecordDigest(tx.BeforeRecord) || tx.AfterRecordSHA != hookRecordDigest(tx.AfterRecord) || tx.Mode&^0777 != 0 || tx.Action == "install" && tx.AfterRecord == nil || tx.Action == "uninstall" && tx.AfterRecord != nil {
		return tx, errors.New("hook recovery journal invalid; configuration unchanged")
	}
	for _, r := range []*HookRecord{tx.BeforeRecord, tx.AfterRecord} {
		if r != nil && (r.Agent != agent || r.Path != tx.Path || r.Managed == "") {
			return tx, errors.New("hook recovery ownership invalid")
		}
	}
	return tx, nil
}
func hookTransactionState(s Store, tx hookTransaction) ([]byte, bool, error) {
	if err := nativeRealParents(tx.Path); err != nil {
		return nil, false, err
	}
	data, err := secureRead(tx.Path, 2*1024*1024)
	exists := !errors.Is(err, os.ErrNotExist)
	if err != nil && exists {
		return nil, false, err
	}
	before := exists == tx.BeforeExists && digest(string(data)) == tx.BeforeSHA
	after := exists && digest(string(data)) == tx.AfterSHA
	if !before && !after {
		return nil, false, errors.New("hook recovery conflicts with user configuration; unchanged")
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
	if !after {
		if err = atomicWrite(tx.Path, tx.After, os.FileMode(tx.Mode)); err != nil {
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
