package passwd

import "testing"

func FuzzParse(f *testing.F) {
	f.Add("$argon2id$v=19$m=65536,t=3,p=4$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY")
	f.Add("$argon2id$v=19$m=4294967295,t=3,p=4$AAAA$AAAA")
	f.Add("")

	f.Fuzz(func(t *testing.T, encoded string) {
		got, err := parse(encoded)
		if err != nil {
			return
		}
		if got.memory != memoryCost || got.time != timeCost || got.threads != threads {
			t.Fatalf("accepted foreign cost m=%d,t=%d,p=%d", got.memory, got.time, got.threads)
		}
		if len(got.salt) != saltLength || len(got.sum) != keyLength {
			t.Fatalf("accepted salt/hash lengths %d/%d", len(got.salt), len(got.sum))
		}
	})
}
