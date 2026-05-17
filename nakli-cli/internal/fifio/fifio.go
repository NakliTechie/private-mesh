// Package fifio reads / writes Fabric Identity Files for the CLI. It wraps
// fabric-sdk-go/identity with the passphrase-prompt UX the spec mandates.
package fifio

import (
	"bufio"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/term"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/identity"
)

// ErrPassphraseMissing is returned when the passphrase prompt fails or stdin
// is closed without a passphrase being supplied.
var ErrPassphraseMissing = errors.New("fifio: passphrase required")

// ErrFIFNotFound is returned when the FIF file does not exist at the given path.
var ErrFIFNotFound = errors.New("fifio: FIF file not found")

// CreateRoot builds a fresh root FIF and writes it to path. Returns the
// generated principal id for caller convenience.
func CreateRoot(path, displayName, passphrase string) (string, error) {
	if displayName == "" {
		return "", fmt.Errorf("CreateRoot: display name is required")
	}
	if passphrase == "" {
		return "", ErrPassphraseMissing
	}
	pub, priv, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		return "", fmt.Errorf("CreateRoot: keygen: %w", err)
	}
	pid, err := ulid.New(ulid.Timestamp(time.Now()), cryptorand.Reader)
	if err != nil {
		return "", fmt.Errorf("CreateRoot: ulid: %w", err)
	}
	inner := identity.NewInnerFIF(
		identity.Principal{
			Type:        identity.PrincipalHuman,
			ID:          pid.String(),
			DisplayName: displayName,
			CreatedAt:   time.Now().UTC(),
		},
		identity.KeyPair{
			Algorithm:  identity.KeyAlgEd25519,
			PublicKey:  pub,
			PrivateKey: priv,
		},
	)
	fif, err := identity.NewFIF(passphrase, inner)
	if err != nil {
		return "", fmt.Errorf("CreateRoot: NewFIF: %w", err)
	}
	defer fif.Lock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("CreateRoot: mkdir parent: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("CreateRoot: open %s: %w", path, err)
	}
	defer f.Close()
	if err := fif.Serialize(f); err != nil {
		return "", fmt.Errorf("CreateRoot: serialize: %w", err)
	}
	return pid.String(), nil
}

// LoadAndUnlock reads the FIF at path and unlocks it with passphrase.
func LoadAndUnlock(path, passphrase string) (*identity.FIF, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrFIFNotFound, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("LoadAndUnlock: open %s: %w", path, err)
	}
	defer f.Close()
	fif, err := identity.ParseFIF(f)
	if err != nil {
		return nil, fmt.Errorf("LoadAndUnlock: parse: %w", err)
	}
	if err := fif.Unlock(passphrase); err != nil {
		return nil, fmt.Errorf("LoadAndUnlock: unlock: %w", err)
	}
	return fif, nil
}

// SaveUnlocked re-encrypts the unlocked FIF at path. The caller's passphrase
// is reused (cached in the FIF after the prior Unlock/NewFIF).
func SaveUnlocked(fif *identity.FIF, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("SaveUnlocked: mkdir parent: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("SaveUnlocked: open %s: %w", tmp, err)
	}
	if err := fif.Serialize(f); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("SaveUnlocked: serialize: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("SaveUnlocked: close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("SaveUnlocked: rename: %w", err)
	}
	return nil
}

// PromptPassphrase reads a passphrase from --passphrase-stdin or interactively
// from the terminal. If `confirm` is true, requires the user to enter the
// passphrase twice and verifies they match.
func PromptPassphrase(stdinMode bool, confirm bool, prompt string) (string, error) {
	if stdinMode {
		r := bufio.NewReader(os.Stdin)
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("PromptPassphrase: read stdin: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return "", ErrPassphraseMissing
		}
		return line, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("PromptPassphrase: stdin is not a terminal; use --passphrase-stdin")
	}
	fmt.Fprint(os.Stderr, prompt+": ")
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("PromptPassphrase: read: %w", err)
	}
	if len(pw) == 0 {
		return "", ErrPassphraseMissing
	}
	if confirm {
		fmt.Fprint(os.Stderr, "Confirm: ")
		pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("PromptPassphrase: confirm: %w", err)
		}
		if string(pw) != string(pw2) {
			return "", fmt.Errorf("passphrases do not match")
		}
	}
	return string(pw), nil
}
