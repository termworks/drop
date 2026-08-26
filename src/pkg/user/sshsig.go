// Package user is who somebody is, as opposed to which machine they are sitting at.
package user

import (
	"crypto/sha512"
	"encoding/pem"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// Signatures in OpenSSH's own format, so a badge can be checked with tools that already exist.
//
// `ssh-keygen -Y sign` and `-Y verify` speak this, which means somebody can audit what drop
// claims without drop being involved, and drop can accept a badge signed by hand. The format is
// PROTOCOL.sshsig from OpenSSH; there is no implementation of it in x/crypto, so here is one.

const (
	magic      = "SSHSIG"
	sigVersion = 1
	hashName   = "sha512"
	pemType    = "SSH SIGNATURE"
)

// wire is the signature blob, armoured into the PEM a person sees.
type wire struct {
	Magic     [6]byte `sshtype:"-"`
	Version   uint32
	PublicKey string
	Namespace string
	Reserved  string
	Hash      string
	Signature string
}

// signed is what is actually put through the key: the message is hashed first, so a key never
// signs an attacker's bytes directly and a large message costs one hash.
type signed struct {
	Namespace string
	Reserved  string
	Hash      string
	Digest    string
}

// Signature is an OpenSSH signature over a message, under drop's namespace.
//
// The namespace is what stops a signature meaning two things: one made for "drop" cannot be
// replayed as a git commit signature or an ssh login, and neither of those can become a badge.
func Signature(by ssh.Signer, message []byte) ([]byte, error) {
	namespace := Namespace

	sum := sha512.Sum512(message)

	blob := append([]byte(magic), ssh.Marshal(signed{
		Namespace: namespace,
		Hash:      hashName,
		Digest:    string(sum[:]),
	})...)

	sig, err := by.Sign(nil, blob)
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: pemType, Bytes: armour(by.PublicKey(), namespace, sig)}), nil
}

// armour builds the signature blob.
func armour(key ssh.PublicKey, namespace string, sig *ssh.Signature) []byte {
	out := []byte(magic)
	out = append(out, ssh.Marshal(struct {
		Version   uint32
		PublicKey string
		Namespace string
		Reserved  string
		Hash      string
		Signature string
	}{
		Version:   sigVersion,
		PublicKey: string(key.Marshal()),
		Namespace: namespace,
		Hash:      hashName,
		Signature: string(ssh.Marshal(sig)),
	})...)

	return out
}

// Verify checks a signature over a message, and reports the key that made it.
//
// Whose key it is, is the caller's business: this says only that whoever holds that key signed
// this message under this namespace.
func Verify(armoured, message []byte, namespace string) (ssh.PublicKey, error) {
	block, _ := pem.Decode(armoured)
	if block == nil || block.Type != pemType {
		return nil, errors.New("that is not an ssh signature")
	}

	body := block.Bytes
	if len(body) < len(magic) || string(body[:len(magic)]) != magic {
		return nil, errors.New("that signature has the wrong shape")
	}

	var held struct {
		Version   uint32
		PublicKey string
		Namespace string
		Reserved  string
		Hash      string
		Signature string
	}
	if err := ssh.Unmarshal(body[len(magic):], &held); err != nil {
		return nil, fmt.Errorf("reading the signature: %w", err)
	}

	if held.Version != sigVersion {
		return nil, fmt.Errorf("signature version %d is not one this understands", held.Version)
	}
	if held.Namespace != namespace {
		return nil, fmt.Errorf("that signature was made for %q, not %q", held.Namespace, namespace)
	}
	if held.Hash != hashName {
		return nil, fmt.Errorf("that signature was hashed with %q", held.Hash)
	}

	key, err := ssh.ParsePublicKey([]byte(held.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("reading the key that signed it: %w", err)
	}

	var sig ssh.Signature
	if err := ssh.Unmarshal([]byte(held.Signature), &sig); err != nil {
		return nil, fmt.Errorf("reading the signature itself: %w", err)
	}

	sum := sha512.Sum512(message)
	blob := append([]byte(magic), ssh.Marshal(signed{
		Namespace: namespace,
		Hash:      hashName,
		Digest:    string(sum[:]),
	})...)

	if err := key.Verify(blob, &sig); err != nil {
		return nil, fmt.Errorf("the signature does not match: %w", err)
	}
	return key, nil
}
