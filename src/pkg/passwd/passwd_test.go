package passwd

import (
	"strings"
	"testing"
)

func TestAPasswordVerifiesAgainstItsHash(t *testing.T) {
	hash, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}
	if !Verify(hash, "correct horse battery staple") {
		t.Fatal("the password did not verify against its own hash")
	}
}

func TestAWrongPasswordDoesNot(t *testing.T) {
	hash, _ := Hash("correct horse battery staple")

	for _, wrong := range []string{"", "wrong", "correct horse battery stapl", "Correct horse battery staple"} {
		if Verify(hash, wrong) {
			t.Fatalf("%q was accepted", wrong)
		}
	}
}

// The same password twice must not produce the same line, or the file tells anyone reading it which
// paths share a secret.
func TestTheSamePasswordHashesDifferently(t *testing.T) {
	first, _ := Hash("same")
	second, _ := Hash("same")

	if first == second {
		t.Fatal("two hashes of one password are identical, so the salt is not doing its job")
	}
	if !Verify(first, "same") || !Verify(second, "same") {
		t.Fatal("both should still verify")
	}
}

// A hash that cannot be read must close the path. Anything else means a corrupted config line is a
// way in.
func TestAnUnreadableHashNeverVerifies(t *testing.T) {
	cases := map[string]string{
		"empty":             "",
		"plaintext":         "hunter2",
		"truncated":         "$argon2id$v=19$m=65536,t=3,p=4$",
		"wrong algorithm":   "$argon2i$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$c29tZWhhc2g",
		"bad base64":        "$argon2id$v=19$m=65536,t=3,p=4$!!!!$!!!!",
		"no version":        "$argon2id$m=65536,t=3,p=4$c29tZXNhbHQ$c29tZWhhc2g",
		"empty hash":        "$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$",
		"not a hash at all": "hello",
	}

	for name, hash := range cases {
		t.Run(name, func(t *testing.T) {
			if Verify(hash, "anything") {
				t.Fatalf("%q let something through", hash)
			}
			if Verify(hash, "") {
				t.Fatalf("%q let an empty password through", hash)
			}
		})
	}
}

func TestAnEmptyPasswordIsRefused(t *testing.T) {
	if _, err := Hash(""); err == nil {
		t.Fatal("an empty password was hashed")
	}
}

// A config given a plaintext password by mistake should be told, rather than quietly never matching.
func TestLooksSpotsAHash(t *testing.T) {
	hash, _ := Hash("x")
	if !Looks(hash) {
		t.Fatal("a real hash was not recognised")
	}
	for _, plain := range []string{"hunter2", "", "$argon2i$something"} {
		if Looks(plain) {
			t.Fatalf("%q was taken for a hash", plain)
		}
	}
}

func TestTheHashIsShapedAsExpected(t *testing.T) {
	hash, _ := Hash("x")

	if n := strings.Count(hash, "$"); n != 5 {
		t.Fatalf("hash has %d separators: %s", n, hash)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("unexpected shape: %s", hash)
	}
}
