package authserver

import "golang.org/x/crypto/bcrypt"

// verifyPassword reports whether the plaintext password matches the stored
// bcrypt hash. It mirrors the Python password_service.verify_password behaviour:
// any error (malformed hash, mismatch) yields false, never a panic.
func verifyPassword(password, hash string) bool {
	if hash == "" {
		return false
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// hashPassword produces a bcrypt hash. Provided for parity/tests; the auth server
// does not create users in this session but tests exercise round-tripping.
func hashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
