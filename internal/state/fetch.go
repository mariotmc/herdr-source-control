// Package state persists fetch bookkeeping shared by the TUI and the one-shot
// fetch command so that both honour a single throttle window.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const errorLimit = 200

// FetchThrottle is the minimum interval between fetches for one repository. The TUI timer
// and the one-shot fetch command share it so they cannot fetch twice in quick succession.
const FetchThrottle = 3 * time.Minute

type Record struct {
	LastAttemptUnix int64  `json:"last_attempt_unix"`
	LastSuccessUnix int64  `json:"last_success_unix"`
	LastError       string `json:"last_error"`
	// LastErrorPermanent marks a failure that every retry reproduces, such as an upstream branch
	// deleted from the remote. Those must not be reported as untrustworthy remote data.
	LastErrorPermanent bool `json:"last_error_permanent,omitempty"`
}

// Stale reports whether the remote data behind this repository's counts can no longer be
// trusted: fetching is failing and has not succeeded within after.
func (r Record) Stale(after time.Duration) bool {
	if r.LastError == "" || r.LastErrorPermanent {
		return false
	}
	return r.LastSuccessUnix == 0 || time.Since(time.Unix(r.LastSuccessUnix, 0)) >= after
}

type Store struct {
	Dir string
}

// Key identifies a checkout by its working-tree root. Worktrees of one repository must not
// share a throttle: a fetch only updates the current branch's upstream, so a shared key would
// let whichever worktree fetched first stop the others from ever refreshing their own branch.
func Key(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])[:16]
}

// Load never fails on a missing, unreadable, or corrupt file: fetch bookkeeping
// must not break a caller that only wants to know when it last fetched.
func (s Store) Load(key string) (Record, error) {
	if s.Dir == "" {
		return Record{}, nil
	}
	data, err := os.ReadFile(s.path(key))
	if err != nil {
		return Record{}, nil
	}
	var record Record
	if json.Unmarshal(data, &record) != nil {
		return Record{}, nil
	}
	return record, nil
}

func (s Store) Save(key string, record Record) error {
	if s.Dir == "" {
		return nil
	}
	record.LastError = sanitize(record.LastError)
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode fetch state: %w", err)
	}
	directory := filepath.Join(s.Dir, "fetch")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create fetch state directory: %w", err)
	}
	temp, err := os.CreateTemp(directory, key+".*.tmp")
	if err != nil {
		return fmt.Errorf("create fetch state file: %w", err)
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("secure fetch state file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write fetch state file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("write fetch state file: %w", err)
	}
	if err := os.Rename(name, s.path(key)); err != nil {
		return fmt.Errorf("replace fetch state file: %w", err)
	}
	return nil
}

func (s Store) path(key string) string {
	return filepath.Join(s.Dir, "fetch", key+".json")
}

func sanitize(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r == utf8.RuneError:
			continue
		case r < 0x20 || r == 0x7f:
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	detail := strings.Join(strings.Fields(b.String()), " ")
	if len(detail) <= errorLimit {
		return detail
	}
	detail = detail[:errorLimit]
	for !utf8.ValidString(detail) {
		detail = detail[:len(detail)-1]
	}
	return detail
}
