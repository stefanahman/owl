// A secret in the config is a reference, never a value. An op:// path
// is read from 1Password once and kept in a file only the user can
// read — the way gh keeps its token on a machine without a keyring —
// so the approval happens once per machine, not once per start. A
// file:// path is read from that file, a $VAR from the environment;
// anything else is taken as the value itself, for tests and for
// machines without 1Password.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// secret is one configured secret: its reference and the name of the
// file that caches it.
type secret struct {
	name    string // the cache file's name: linear → linear.token
	ref     string // op://…, $VAR, or the value
	account string // the 1Password account holding the item, when several are signed in
	// notify tells the user why a 1Password prompt is about to appear,
	// where the multiplexer shows messages; nil for none.
	notify func(string)
}

// errNoSecret is a reference left empty in the config.
var errNoSecret = errors.New("no token configured")

// cachePath is $XDG_STATE_HOME/owl/<name>.token, else
// ~/.local/state/owl/<name>.token.
func (s secret) cachePath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "owl", s.name+".token"), nil
}

// value resolves the reference: the cache when it holds the value,
// 1Password once otherwise.
func (s secret) value() (string, error) {
	switch {
	case s.ref == "":
		return "", errNoSecret
	case strings.HasPrefix(s.ref, "$"):
		if v := os.Getenv(s.ref[1:]); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("%s is not set", s.ref)
	case strings.HasPrefix(s.ref, "file://"):
		v, err := readCached(expandHome(strings.TrimPrefix(s.ref, "file://")))
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s: no such file, or empty", s.ref)
		}
		return v, err
	case !strings.HasPrefix(s.ref, "op://"):
		return s.ref, nil
	}
	path, err := s.cachePath()
	if err != nil {
		return "", err
	}
	if v, err := readCached(path); err == nil {
		return v, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if s.notify != nil {
		s.notify(fmt.Sprintf("owl reads the %s token from 1Password — once; it is kept in %s", s.name, path))
	}
	args := []string{"read", s.ref}
	if s.account != "" {
		args = append(args, "--account", s.account)
	}
	out, err := runOut(exec.Command("op", args...))
	if err != nil {
		return "", fmt.Errorf("1Password: %w", err)
	}
	if out == "" {
		return "", fmt.Errorf("1Password: %s is empty", s.ref)
	}
	if err := writeCached(path, out); err != nil {
		return "", err
	}
	return out, nil
}

// forget drops the cached value: the next value() asks 1Password
// again. For a token the service has refused.
func (s secret) forget() error {
	path, err := s.cachePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// readCached returns a file's value, refusing a file others can read.
func readCached(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s is readable by others (mode %04o); chmod 600 it", path, st.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "", fs.ErrNotExist
	}
	return v, nil
}

// writeCached writes the value for the user's eyes only, atomically.
func writeCached(path, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(value+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
