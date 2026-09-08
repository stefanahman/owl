package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeOp puts an `op` on PATH that answers `op read <ref>` with a value
// and counts its calls in a file.
func fakeOp(t *testing.T, value string) (calls func() int) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$1 $2\" in 'read op://Vault/Linear/credential') echo '" + value + "';; *) echo 'op: no such item' >&2; exit 1;; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "op"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		data, _ := os.ReadFile(log)
		return strings.Count(string(data), "\n")
	}
}

func TestSecretFromOnePasswordIsReadOnceAndKept(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	calls := fakeOp(t, "lin_api_secret")
	var notes []string
	s := secret{name: "linear", ref: "op://Vault/Linear/credential", notify: func(text string) { notes = append(notes, text) }}
	for range 3 {
		v, err := s.value()
		if err != nil || v != "lin_api_secret" {
			t.Fatalf("value = %q, %v", v, err)
		}
	}
	if calls() != 1 {
		t.Errorf("op read called %d times, want once", calls())
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "1Password") || !strings.Contains(notes[0], "linear.token") {
		t.Errorf("the reason was announced %d times: %v", len(notes), notes)
	}
	path := filepath.Join(state, "owl", "linear.token")
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("cache %s: %v, mode %v", path, err, st.Mode())
	}
	if dir, _ := os.Stat(filepath.Dir(path)); dir.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode %v, want 0700", dir.Mode().Perm())
	}
	// Forgotten: the next value asks again.
	if err := s.forget(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.value(); err != nil || calls() != 2 {
		t.Errorf("after forget: %v, op calls %d", err, calls())
	}
}

func TestSecretRefusesACacheOthersCanRead(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	fakeOp(t, "x")
	path := filepath.Join(state, "owl", "linear.token")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("leaked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := secret{name: "linear", ref: "op://Vault/Linear/credential"}
	if _, err := s.value(); err == nil || !strings.Contains(err.Error(), "readable by others") {
		t.Errorf("got %v, want a refusal", err)
	}
}

func TestSecretOtherForms(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("OWL_TEST_TOKEN", "from-env")
	if v, err := (secret{name: "x", ref: "$OWL_TEST_TOKEN"}).value(); err != nil || v != "from-env" {
		t.Errorf("$VAR: %q, %v", v, err)
	}
	if _, err := (secret{name: "x", ref: "$OWL_TEST_UNSET"}).value(); err == nil {
		t.Error("an unset variable should fail")
	}
	if v, err := (secret{name: "x", ref: "literal-token"}).value(); err != nil || v != "literal-token" {
		t.Errorf("literal: %q, %v", v, err)
	}
	if _, err := (secret{name: "x"}).value(); err != errNoSecret {
		t.Errorf("empty: %v", err)
	}
	// The account, when several are signed in, reaches op.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "op"), []byte("#!/bin/sh\necho \"$*\" > "+filepath.Join(dir, "args")+"\necho key\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if v, err := (secret{name: "acct", ref: "op://Employee/Linear/credential", account: "bardotechnology.1password.eu"}).value(); err != nil || v != "key" {
		t.Fatalf("with an account: %q, %v", v, err)
	}
	if args, _ := os.ReadFile(filepath.Join(dir, "args")); strings.TrimSpace(string(args)) != "read op://Employee/Linear/credential --account bardotechnology.1password.eu" {
		t.Errorf("op args = %q", args)
	}
	// A failing op names 1Password.
	fakeOp(t, "x")
	if _, err := (secret{name: "x", ref: "op://Vault/Other/credential"}).value(); err == nil || !strings.Contains(err.Error(), "1Password") {
		t.Errorf("op failure: %v", err)
	}
}
