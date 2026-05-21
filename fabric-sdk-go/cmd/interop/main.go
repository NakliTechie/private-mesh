// Command interop generates and verifies cross-SDK fixtures for the M1 gate.
// It reads ../interop-tests/m1-vectors.json (resolved relative to repo root)
// and writes fabric/macaroon fixtures under ../interop-tests/m1/from-go/, then
// verifies fixtures under ../interop-tests/m1/from-js/ if present.
//
// Run via scripts/m1-interop.sh, or directly:
//   go run ./cmd/interop -mode=generate    -dir ../interop-tests/m1
//   go run ./cmd/interop -mode=verify      -dir ../interop-tests/m1
//   go run ./cmd/interop -mode=re-serialize -in <fif.bin> -out <fif.bin>
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/NakliTechie/private-mesh/fabric-sdk-go/grant"
	"github.com/NakliTechie/private-mesh/fabric-sdk-go/identity"
)

type vectors struct {
	FIF struct {
		Passphrase string `json:"passphrase"`
		Principal  struct {
			Type        string `json:"type"`
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			CreatedAt   string `json:"created_at"`
		} `json:"principal"`
		RootKeypair struct {
			Algorithm     string `json:"algorithm"`
			PublicKeyHex  string `json:"public_key_hex"`
			PrivateKeyHex string `json:"private_key_hex"`
		} `json:"root_keypair"`
	} `json:"fif"`
	Macaroon struct {
		RootKeyHex string `json:"root_key_hex"`
		Location   string `json:"location"`
		Identifier struct {
			GrantID           string `json:"grant_id"`
			IssuedAt          string `json:"issued_at"`
			IssuedByPrincipal string `json:"issued_by_principal"`
			IssuedByKeypair   string `json:"issued_by_keypair_hex"`
			Scope             struct {
				Primitive  string   `json:"primitive"`
				Namespace  string   `json:"namespace"`
				Operations []string `json:"operations"`
			} `json:"scope"`
		} `json:"identifier"`
		Caveats []string `json:"caveats"`
	} `json:"macaroon"`
}

func main() {
	mode := flag.String("mode", "", "generate | verify | re-serialize")
	dir := flag.String("dir", "", "interop-tests/m1 root directory (generate/verify)")
	in := flag.String("in", "", "input fif.bin (re-serialize)")
	out := flag.String("out", "", "output fif.bin (re-serialize)")
	vectorsPath := flag.String("vectors", "", "path to m1-vectors.json (default: <dir>/../m1-vectors.json, or alongside -in)")
	flag.Parse()

	switch *mode {
	case "generate", "verify":
		if *dir == "" {
			flag.Usage()
			os.Exit(2)
		}
	case "re-serialize":
		if *in == "" || *out == "" {
			flag.Usage()
			os.Exit(2)
		}
	default:
		flag.Usage()
		os.Exit(2)
	}

	if *vectorsPath == "" {
		switch *mode {
		case "generate", "verify":
			*vectorsPath = filepath.Join(filepath.Dir(*dir), "m1-vectors.json")
		case "re-serialize":
			// -in is .../interop-tests/m1/<sub>/fif.bin; vectors live at .../interop-tests/m1-vectors.json
			*vectorsPath = filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(*in))), "m1-vectors.json")
		}
	}

	v, err := readVectors(*vectorsPath)
	if err != nil {
		log.Fatalf("read vectors: %v", err)
	}

	switch *mode {
	case "generate":
		outDir := filepath.Join(*dir, "from-go")
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			log.Fatal(err)
		}
		if err := generateFIF(v, filepath.Join(outDir, "fif.bin")); err != nil {
			log.Fatalf("generate FIF: %v", err)
		}
		if err := generateMacaroon(v, filepath.Join(outDir, "macaroon.bin")); err != nil {
			log.Fatalf("generate macaroon: %v", err)
		}
		fmt.Println("Go interop: wrote", outDir)
	case "verify":
		inDir := filepath.Join(*dir, "from-js")
		if _, err := os.Stat(inDir); err != nil {
			log.Fatalf("verify: %s not present (run JS generate first)", inDir)
		}
		if err := verifyFIF(v, filepath.Join(inDir, "fif.bin")); err != nil {
			log.Fatalf("verify FIF: %v", err)
		}
		if err := verifyMacaroon(v, filepath.Join(inDir, "macaroon.bin")); err != nil {
			log.Fatalf("verify macaroon: %v", err)
		}
		fmt.Println("Go interop: verified", inDir)
	case "re-serialize":
		if err := reSerializeFIF(v, *in, *out); err != nil {
			log.Fatalf("re-serialize FIF: %v", err)
		}
		fmt.Println("Go interop: re-serialized", *in, "->", *out)
	}
}

func readVectors(p string) (*vectors, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var v vectors
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func generateFIF(v *vectors, out string) error {
	pub, err := hex.DecodeString(v.FIF.RootKeypair.PublicKeyHex)
	if err != nil {
		return fmt.Errorf("decode public_key_hex: %w", err)
	}
	priv, err := hex.DecodeString(v.FIF.RootKeypair.PrivateKeyHex)
	if err != nil {
		return fmt.Errorf("decode private_key_hex: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339, v.FIF.Principal.CreatedAt)
	if err != nil {
		return fmt.Errorf("parse created_at: %w", err)
	}
	inner := identity.NewInnerFIF(
		identity.Principal{
			Type:        identity.PrincipalType(v.FIF.Principal.Type),
			ID:          v.FIF.Principal.ID,
			DisplayName: v.FIF.Principal.DisplayName,
			CreatedAt:   createdAt,
		},
		identity.KeyPair{
			Algorithm:  identity.KeyAlgorithm(v.FIF.RootKeypair.Algorithm),
			PublicKey:  pub,
			PrivateKey: priv,
		},
	)
	fif, err := identity.NewFIF(v.FIF.Passphrase, inner)
	if err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	return fif.Serialize(f)
}

func verifyFIF(v *vectors, in string) error {
	f, err := os.Open(in)
	if err != nil {
		return err
	}
	defer f.Close()
	fif, err := identity.ParseFIF(f)
	if err != nil {
		return fmt.Errorf("ParseFIF: %w", err)
	}
	if err := fif.Unlock(v.FIF.Passphrase); err != nil {
		return fmt.Errorf("Unlock: %w", err)
	}
	if fif.Inner.Principal.ID != v.FIF.Principal.ID {
		return fmt.Errorf("principal.id mismatch: got %q, want %q", fif.Inner.Principal.ID, v.FIF.Principal.ID)
	}
	if fif.Inner.Principal.DisplayName != v.FIF.Principal.DisplayName {
		return fmt.Errorf("principal.display_name mismatch: got %q, want %q", fif.Inner.Principal.DisplayName, v.FIF.Principal.DisplayName)
	}
	expectedPub, _ := hex.DecodeString(v.FIF.RootKeypair.PublicKeyHex)
	if hex.EncodeToString(fif.Inner.RootKeypair.PublicKey) != hex.EncodeToString(expectedPub) {
		return fmt.Errorf("root public key mismatch")
	}
	return nil
}

func generateMacaroon(v *vectors, out string) error {
	rk, err := hex.DecodeString(v.Macaroon.RootKeyHex)
	if err != nil {
		return fmt.Errorf("decode root_key_hex: %w", err)
	}
	pub, err := hex.DecodeString(v.Macaroon.Identifier.IssuedByKeypair)
	if err != nil {
		return fmt.Errorf("decode issued_by_keypair_hex: %w", err)
	}
	issuedAt, err := time.Parse(time.RFC3339, v.Macaroon.Identifier.IssuedAt)
	if err != nil {
		return fmt.Errorf("parse issued_at: %w", err)
	}
	g, err := grant.Mint(grant.MintSpec{
		RootKey:  rk,
		Location: v.Macaroon.Location,
		Identifier: grant.Identifier{
			GrantID:           v.Macaroon.Identifier.GrantID,
			IssuedAt:          issuedAt,
			IssuedByPrincipal: v.Macaroon.Identifier.IssuedByPrincipal,
			IssuedByKeypair:   pub,
			Scope: grant.Scope{
				Primitive:  grant.Primitive(v.Macaroon.Identifier.Scope.Primitive),
				Namespace:  v.Macaroon.Identifier.Scope.Namespace,
				Operations: v.Macaroon.Identifier.Scope.Operations,
			},
		},
		Caveats: v.Macaroon.Caveats,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(out, g.Macaroon, 0o644)
}

// reSerializeFIF reads a FIF, unlocks it, enrolls a deterministic device
// subkey (so both SDKs produce byte-identical inner content), then writes
// the FIF back out under a fresh AEAD nonce. The other SDK's verify must
// still decrypt — proving cross-SDK AAD binding survives nonce rotation.
func reSerializeFIF(v *vectors, in, out string) error {
	inBytes, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	fif, err := identity.ParseFIF(bytes.NewReader(inBytes))
	if err != nil {
		return fmt.Errorf("ParseFIF: %w", err)
	}
	if err := fif.Unlock(v.FIF.Passphrase); err != nil {
		return fmt.Errorf("Unlock: %w", err)
	}
	enrolledAt, _ := time.Parse(time.RFC3339, "2026-05-21T00:00:00Z")
	fif.Inner.DeviceSubkeys = append(fif.Inner.DeviceSubkeys, identity.DeviceSubkey{
		DeviceID:   "01JINTEROPDEVICETESTM10001",
		DeviceName: "interop-mutator",
		Algorithm:  identity.KeyAlgEd25519,
		PublicKey:  bytes.Repeat([]byte{0x55}, 32),
		PrivateKey: bytes.Repeat([]byte{0x66}, 64),
		EnrolledAt: enrolledAt,
	})
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	return fif.Serialize(f)
}

func verifyMacaroon(v *vectors, in string) error {
	macBytes, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	rk, err := hex.DecodeString(v.Macaroon.RootKeyHex)
	if err != nil {
		return err
	}
	if err := grant.VerifySignature(macBytes, rk, grant.AlwaysSatisfied); err != nil {
		return fmt.Errorf("VerifySignature: %w", err)
	}
	g, err := grant.Parse(macBytes)
	if err != nil {
		return fmt.Errorf("Parse: %w", err)
	}
	if g.Identifier.GrantID != v.Macaroon.Identifier.GrantID {
		return fmt.Errorf("grant_id mismatch: got %q, want %q", g.Identifier.GrantID, v.Macaroon.Identifier.GrantID)
	}
	if string(g.Identifier.Scope.Primitive) != v.Macaroon.Identifier.Scope.Primitive {
		return fmt.Errorf("primitive mismatch: got %q, want %q", g.Identifier.Scope.Primitive, v.Macaroon.Identifier.Scope.Primitive)
	}
	if len(g.Caveats) != len(v.Macaroon.Caveats) {
		return fmt.Errorf("caveat count: got %d, want %d", len(g.Caveats), len(v.Macaroon.Caveats))
	}
	for i, want := range v.Macaroon.Caveats {
		if g.Caveats[i] != want {
			return fmt.Errorf("caveat[%d]: got %q, want %q", i, g.Caveats[i], want)
		}
	}
	return nil
}
