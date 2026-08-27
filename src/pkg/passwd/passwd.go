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

	var version int
	if _, err := fmt.Sscanf(field[2], "v=%d", &version); err != nil {
		return out, fmt.Errorf("unreadable version: %w", err)
	}
	if version != argon2.Version {
		return out, fmt.Errorf("argon2 version %d, expected %d", version, argon2.Version)
	}
	if _, err := fmt.Sscanf(field[3], "m=%d,t=%d,p=%d", &out.memory, &out.time, &out.threads); err != nil {
		return out, fmt.Errorf("unreadable cost: %w", err)
	}

	salt, err := decode(field[4])
	if err != nil {
		return out, fmt.Errorf("unreadable salt: %w", err)
	}
	sum, err := decode(field[5])
	if err != nil {
		return out, fmt.Errorf("unreadable hash: %w", err)
	}
	if len(sum) == 0 {
		return out, fmt.Errorf("the hash is empty")
	}

	out.salt, out.sum = salt, sum
	return out, nil
}

func encode(raw []byte) string           { return base64.RawStdEncoding.EncodeToString(raw) }
func decode(text string) ([]byte, error) { return base64.RawStdEncoding.DecodeString(text) }
