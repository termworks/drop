package passwd

import (
	"strings"
	"sync"
	"testing"
	"time"
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

// A path guarded by a password is reachable by anybody who knows this device's id, so the guessing
// happens on somebody else's machine and the 64 MiB is committed on this one. What one session
// offers must be hashed once, however many times it is asked about.
func TestAGuessIsHashedOnce(t *testing.T) {
	hash, err := Hash("open sesame")
	if err != nil {
		t.Fatal(err)
	}

	tried := NewTried()

	once := time.Now()
	if !tried.Says(hash, "open sesame") {
		t.Fatal("the right password was refused")
	}
	first := time.Since(once)

	again := time.Now()
	for i := 0; i < 50; i++ {
		if !tried.Says(hash, "open sesame") {
			t.Fatal("the right password was refused on the way back")
		}
	}
	rest := time.Since(again)

	if rest > first {
		t.Errorf("fifty remembered answers took %v, one hash took %v", rest, first)
	}

	// A different guess is a different question and is really asked.
	if tried.Says(hash, "not it") {
		t.Error("a wrong password was admitted")
	}
	// And a nil memory still answers, it just pays every time.
	var none *Tried
	if !none.Says(hash, "open sesame") {
		t.Error("a caller with no memory could not be let in")
	}
}

// However many are guessing at once, only so much memory is committed to it.
func TestOnlySoManyGuessesRunAtOnce(t *testing.T) {
	hash, err := Hash("crowded")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Verify(hash, "crowded")
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("twenty-four guesses did not finish; the queue is not draining")
	}
}
