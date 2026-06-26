// One-shot generator for the cross-language wire-format fixture. Run
// by hand:
//
//	go run ./internal/runtimecrypto/cmd/emit-fixture > internal/runtimecrypto/testdata/wire_v1.json
//
// The output is COMMITTED and read-only at test time. `//go:build
// ignore` keeps this invisible to `go build ./...` / `go test ./...`,
// and the stderr WARNING forces the operator to notice they are about
// to mutate a wire-locked artefact. A *_test.go that wrote the file
// would silently green-light protocol drift on every CI run.

//go:build ignore

package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	runtimecrypto "github.com/MiniMax-AI-Dev/parsar/internal/runtimecrypto"
	"golang.org/x/crypto/nacl/box"
)

type fixture struct {
	Description      string `json:"description"`
	WireFormat       string `json:"wire_format"`
	RecipientPubB64  string `json:"recipient_public_key_b64"`
	RecipientPrivB64 string `json:"recipient_private_key_b64"`
	CipherB64        string `json:"cipher_b64"`
	PlaintextUTF8    string `json:"plaintext_utf8"`
}

func main() {
	fmt.Fprintln(os.Stderr,
		"WARNING: this regenerates internal/runtimecrypto/testdata/wire_v1.json.")
	fmt.Fprintln(os.Stderr,
		"  Only commit the new bytes if you intentionally bumped the wire protocol version.")
	fmt.Fprintln(os.Stderr,
		"  Re-generating without a version bump silently breaks the protocol-version lock.")

	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "keygen: %v\n", err)
		os.Exit(1)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub[:])
	privB64 := base64.StdEncoding.EncodeToString(priv[:])

	plaintext := `{"api_key":"sk-wire-fixture","provider":"anthropic"}`
	cipherB64, err := runtimecrypto.SealForRuntime([]byte(plaintext), pubB64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seal: %v\n", err)
		os.Exit(1)
	}

	out := fixture{
		Description: "Wire fixture for Parsar runtime credential envelope. " +
			"Recipient keypair is committed for test reproducibility. DO NOT use these " +
			"keys in any other context. Wire format: NaCl SealAnonymous (X25519 + " +
			"XSalsa20-Poly1305), nonce = BLAKE2b-24(ephPub || recipientPub).",
		WireFormat:       "nacl_sealed_box_v1",
		RecipientPubB64:  pubB64,
		RecipientPrivB64: privB64,
		CipherB64:        cipherB64,
		PlaintextUTF8:    plaintext,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
}
