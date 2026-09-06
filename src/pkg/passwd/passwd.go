// Package passwd hashes and checks the secrets that guard a path.
//
// A config file is read by anything that can read the file, and gets copied into dotfile repositories
// and backups. So what it holds is a hash, and the plaintext exists only in the head of whoever is
// about to type it.
package passwd

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// The cost of one guess. Deliberately expensive: the whole value of a hash is that trying the
// dictionary takes longer than the attacker is willing to wait.
const (
	timeCost   = 3
	memoryCost = 64 * 1024
	threads    = 4
	keyLength  = 32
	saltLength = 16
)

// Hash turns a password into something safe to write down.
func Hash(plain string) (string, error) {
	if plain == "" {
		return "", fmt.Errorf("an empty password guards nothing")
	}

	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating a salt: %w", err)
	}

	sum := argon2.IDKey([]byte(plain), salt, timeCost, memoryCost, threads, keyLength)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memoryCost, timeCost, threads,
		encode(salt), encode(sum)), nil
}

// Verify reports whether a password is the one a hash was made from.
//
// A hash it cannot read is a failure, never a pass: a corrupted line in a config must close a path,
// not open it.
func Verify(hash, plain string) bool {
	parsed, err := parse(hash)
	if err != nil {
		return false
	}

	var sum []byte
	spend(func() {
		sum = argon2.IDKey([]byte(plain), parsed.salt, parsed.time, parsed.memory, parsed.threads, uint32(len(parsed.sum)))
	})
	return subtle.ConstantTimeCompare(sum, parsed.sum) == 1
}

// Looks reports whether text is shaped like one of these hashes, so a config that was given a
// plaintext password by mistake can be told so rather than silently never matching.
func Looks(text string) bool {
	return strings.HasPrefix(text, "$argon2id$")
}

type parts struct {
	memory, time uint32
	threads      uint8
	salt, sum    []byte
}

func parse(hash string) (parts, error) {
	var out parts

	field := strings.Split(hash, "$")
	if len(field) != 6 || field[0] != "" || field[1] != "argon2id" {
		return out, fmt.Errorf("not an argon2id hash")
	}

	if field[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return out, fmt.Errorf("unsupported argon2 version")
	}
	wantCost := fmt.Sprintf("m=%d,t=%d,p=%d", memoryCost, timeCost, threads)
	if field[3] != wantCost {
		return out, fmt.Errorf("unsupported argon2 cost")
	}
	if len(field[4]) != base64.RawStdEncoding.EncodedLen(saltLength) {
		return out, fmt.Errorf("unexpected salt length")
	}
	if len(field[5]) != base64.RawStdEncoding.EncodedLen(keyLength) {
		return out, fmt.Errorf("unexpected hash length")
	}

	salt, err := decode(field[4])
	if err != nil {
		return out, fmt.Errorf("unreadable salt: %w", err)
	}
	sum, err := decode(field[5])
	if err != nil {
		return out, fmt.Errorf("unreadable hash: %w", err)
	}
	if len(salt) != saltLength || len(sum) != keyLength {
		return out, fmt.Errorf("unexpected decoded length")
	}

	out.memory, out.time, out.threads = memoryCost, timeCost, threads
	out.salt, out.sum = salt, sum
	return out, nil
}

func encode(raw []byte) string           { return base64.RawStdEncoding.EncodeToString(raw) }
func decode(text string) ([]byte, error) { return base64.RawStdEncoding.DecodeString(text) }
