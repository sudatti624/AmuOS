package security

import "testing"

func TestPasswordHash(t *testing.T) {
	password := "correct horse battery staple"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if hash == password || !VerifyPassword(password, hash) || VerifyPassword("wrong password", hash) {
		t.Fatal("password verification failed")
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if hash == second {
		t.Fatal("salt reused")
	}
	for _, bad := range []string{"", "$argon2id$v=19$m=999999999,t=3,p=2$x$x", "$argon2id$v=19$m=65536,t=3,p=2$bad$bad"} {
		if VerifyPassword(password, bad) {
			t.Fatal("malformed hash accepted")
		}
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestTokenDigest(t *testing.T) {
	a, b := Token(), Token()
	if a == b || len(a) != 43 {
		t.Fatal("invalid random tokens")
	}
	if !EqualDigest(a, Digest(a)) || EqualDigest(b, Digest(a)) {
		t.Fatal("invalid digest comparison")
	}
}
