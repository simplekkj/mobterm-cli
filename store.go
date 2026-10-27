package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Pair struct {
	SetupPacket
	Status string `json:"status"`
}
type Store struct{ Dir string }

func defaultStore() (Store, error) {
	d := os.Getenv("MOBTERM_CONFIG_DIR")
	if d == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Store{}, err
		}
		d = filepath.Join(h, ".config", "mobterm")
	}
	a, err := filepath.Abs(d)
	return Store{a}, err
}
func ensureDir(path string, private bool) error {
	i, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err = ensureDir(filepath.Dir(path), false); err != nil {
			return err
		}
		if err = os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		i, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return errors.New("configuration path is not a real directory")
	}
	if private && i.Mode().Perm()&0077 != 0 {
		return errors.New("configuration directory must have mode 0700")
	}
	return nil
}
func secureRead(path string, limit int64) ([]byte, error) {
	data, _, err := secureReadWithMode(path, limit)
	return data, err
}

// Read content and permissions from the same no-follow, regular-file handle.
func secureReadWithMode(path string, limit int64) ([]byte, os.FileMode, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	i, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !i.Mode().IsRegular() {
		return nil, 0, errors.New("configuration is not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(b)) > limit {
		return nil, 0, errors.New("configuration exceeds size limit")
	}
	return b, i.Mode().Perm(), nil
}
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if i, err := os.Lstat(path); err == nil {
		if !i.Mode().IsRegular() {
			return errors.New("refusing non-regular destination")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".mobterm-write-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(mode.Perm()); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s Store) lock(ctx context.Context) (func(), error) {
	if err := ensureDir(s.Dir, true); err != nil {
		return nil, err
	}
	return lockFile(ctx, filepath.Join(s.Dir, ".lock"))
}
func lockFile(ctx context.Context, path string) (func(), error) {
	if ctx.Err() != nil {
		return nil, errors.New("command_budget_exhausted")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, errors.New("configuration busy")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func (s Store) readJSON(name string, v any) error {
	b, err := secureRead(filepath.Join(s.Dir, name), 2*1024*1024)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func (s Store) writeJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.Dir, name), append(b, '\n'), 0600)
}
func (s Store) installation() (string, error) {
	ctx, c := context.WithTimeout(context.Background(), 2*time.Second)
	defer c()
	unlock, err := s.lock(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	var v struct {
		V  int    `json:"v"`
		ID string `json:"installation_id"`
	}
	err = s.readJSON("installation.json", &v)
	if err == nil {
		if v.V != 1 || !opaqueID.MatchString(v.ID) {
			return "", errors.New("invalid installation identity")
		}
		return v.ID, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	v.V = 1
	v.ID = randomID()
	return v.ID, s.writeJSON("installation.json", v)
}
func (s Store) pairs() (map[string]Pair, error) {
	v := map[string]Pair{}
	err := s.readJSON("pairs.json", &v)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return v, err
}
func (s Store) savePair(ctx context.Context, p Pair) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return s.savePairLocked(p)
}
func (s Store) transitionPair(ctx context.Context, expected Pair, status string) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	pairs, err := s.pairs()
	if err != nil {
		return err
	}
	if current, ok := pairs[expected.PairID]; !ok || current != expected {
		return errors.New("pair_operation_superseded")
	}
	next := expected
	next.Status = status
	return s.savePairLocked(next)
}
func (s Store) savePairLocked(p Pair) error {
	v, err := s.pairs()
	if err != nil {
		return err
	}
	var retired []retiredPair
	if e := s.readJSON("pair-history.json", &retired); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	for _, summary := range retired {
		if summary.PairRef == digest(p.PairID) {
			return errors.New("pair_retired_new_pair_required")
		}
	}
	if old, ok := v[p.PairID]; ok && old.SetupPacket != p.SetupPacket {
		return errors.New("pair identity conflict")
	}
	active, pending := 0, 0
	for id, old := range v {
		if id == p.PairID {
			continue
		}
		if old.Status == "pending" || old.Status == "pending_review" {
			pending++
		} else {
			active++
		}
	}
	if p.Status == "pending" || p.Status == "pending_review" {
		if pending >= 32 {
			return errors.New("pending_pair_limit_reconcile_or_abandon")
		}
	} else if active >= 32 {
		return errors.New("active_pair_limit")
	}

	v[p.PairID] = p
	return s.writeJSON("pairs.json", v)
}
