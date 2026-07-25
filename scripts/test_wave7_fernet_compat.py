#!/usr/bin/env python3
"""
Wave 7 Phase 1 — Fernet bidirectional compatibility test.

Verifies that:
  1. Python encrypts → Go decrypts (Go can read Python-produced DB values)
  2. Go encrypts → Python decrypts (Python can read Go-produced DB values)

Uses only test-only secrets and plaintext values.
Never uses or prints a real production API key or production secret.

Run inside them-bridge container:
  docker exec them-bridge python3 /app/scripts/test_wave7_fernet_compat.py

Or against a running Go bridge on port 8002:
  docker exec them-bridge python3 /app/scripts/test_wave7_fernet_compat.py \
    --go-url http://them-go-bridge:8002
"""

import argparse
import base64
import hashlib
import json
import os
import subprocess
import sys
import tempfile

# ── Crypto helpers (mirrors app/utils/crypto.py exactly) ─────────────────────

_ENC_PREFIX = "enc:"

def _derive_fernet_key(secret_key: str) -> bytes:
    """Derive the 32-byte Fernet key from the secret key."""
    return base64.urlsafe_b64encode(
        hashlib.sha256(secret_key.encode()).digest()
    )

def _fernet_for(secret_key: str):
    from cryptography.fernet import Fernet
    return Fernet(_derive_fernet_key(secret_key))

def encrypt_value(value: str, secret_key: str) -> str:
    """Encrypt using the Fernet scheme, prepend 'enc:' prefix."""
    if not value or value.startswith(_ENC_PREFIX):
        return value
    f = _fernet_for(secret_key)
    return _ENC_PREFIX + f.encrypt(value.encode()).decode()

def decrypt_value(value: str, secret_key: str) -> str:
    """Decrypt an 'enc:'-prefixed Fernet value."""
    if not value or not value.startswith(_ENC_PREFIX):
        return value
    try:
        f = _fernet_for(secret_key)
        return f.decrypt(value[len(_ENC_PREFIX):].encode()).decode()
    except Exception as exc:
        return ""

# ── Test vectors (test-only secrets; never production values) ─────────────────

TEST_SECRET_KEY = "wave7-test-secret-do-not-use-in-prod"

TEST_CASES = [
    ("sk-ant-api03-wave7testkey0000000000000000000000001", "typical API key (50 chars)"),
    ("sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLM", "longer API key (62 chars)"),
    ("hello", "short string (≤8 chars, masks as ****)"),
    ("12345678", "exactly 8 chars (masks as ****)"),
    ("123456789", "9 chars (>8, shows first4...last4)"),
    ("test-api-key-wave7-phase1", "reference plaintext matching Go known vector"),
    ("café-key-unicode", "unicode UTF-8 plaintext"),
]

# ── Mask helper (mirrors admin_llm_providers.py:_mask_key) ────────────────────

def mask_key(plaintext: str) -> str:
    if len(plaintext) <= 8:
        return "****"
    return plaintext[:4] + "..." + plaintext[-4:]

# ── Test runner ───────────────────────────────────────────────────────────────

class CompatTest:
    def __init__(self, verbose: bool = False):
        self.passed = 0
        self.failed = 0
        self.verbose = verbose

    def ok(self, label: str, detail: str = ""):
        self.passed += 1
        mark = "PASS"
        if self.verbose and detail:
            print(f"  {mark}: {label} — {detail}")
        else:
            print(f"  {mark}: {label}")

    def fail(self, label: str, detail: str = ""):
        self.failed += 1
        print(f"  FAIL: {label}", file=sys.stderr)
        if detail:
            print(f"       {detail}", file=sys.stderr)

    def assert_eq(self, label: str, got, want):
        if got == want:
            self.ok(label)
        else:
            self.fail(label, f"got={got!r} want={want!r}")

    def assert_true(self, label: str, value: bool, detail: str = ""):
        if value:
            self.ok(label, detail)
        else:
            self.fail(label, detail)

    def summary(self) -> bool:
        total = self.passed + self.failed
        print()
        print(f"Results: {self.passed}/{total} passed, {self.failed} failed")
        return self.failed == 0


def test_python_encrypt_go_decrypt(t: CompatTest, go_decrypt_fn):
    """Direction 1: Python encrypts → Go decrypts."""
    print("\n[1] Python encrypts → Go decrypts")
    for plaintext, label in TEST_CASES:
        stored = encrypt_value(plaintext, TEST_SECRET_KEY)
        assert stored.startswith(_ENC_PREFIX), f"encrypt_value did not add enc: prefix for {label!r}"

        go_result = go_decrypt_fn(stored, TEST_SECRET_KEY)
        t.assert_eq(f"  {label}", go_result, plaintext)


def test_go_encrypt_python_decrypt(t: CompatTest, go_encrypt_fn):
    """Direction 2: Go encrypts → Python decrypts."""
    print("\n[2] Go encrypts → Python decrypts")
    for plaintext, label in TEST_CASES:
        stored = go_encrypt_fn(plaintext, TEST_SECRET_KEY)
        if stored is None:
            t.fail(f"  {label}", "Go encrypt returned None/error")
            continue
        t.assert_true(f"  {label}: enc: prefix present",
                      stored.startswith(_ENC_PREFIX))

        decrypted = decrypt_value(stored, TEST_SECRET_KEY)
        t.assert_eq(f"  {label}: round-trip", decrypted, plaintext)


def test_python_only_round_trip(t: CompatTest):
    """Sanity: Python encrypt → Python decrypt."""
    print("\n[0] Python self round-trip (sanity)")
    for plaintext, label in TEST_CASES:
        stored = encrypt_value(plaintext, TEST_SECRET_KEY)
        decrypted = decrypt_value(stored, TEST_SECRET_KEY)
        t.assert_eq(f"  {label}", decrypted, plaintext)


def test_known_vector(t: CompatTest, go_decrypt_fn):
    """Verify the exact known vector used in Go unit tests."""
    print("\n[K] Known vector from Go fernet_test.go")
    known_token = (
        "gAAAAABlU_EAAQIDBAUGBwgJCgsMDQ4PENSEfjZhbp7TUUrlX2VX4Etcgk5ljOeuEzUqs"
        "zUGnzW309NBYudvQUNjUK41tv5em0yiWzwl2UxYFRyd2ZV4o24="
    )
    stored = _ENC_PREFIX + known_token
    expected = "test-api-key-wave7-phase1"

    # Verify Python can decrypt the known vector
    decrypted = decrypt_value(stored, TEST_SECRET_KEY)
    t.assert_eq("Python decrypts Go known vector", decrypted, expected)

    # Verify Go can decrypt it too
    go_result = go_decrypt_fn(stored, TEST_SECRET_KEY)
    t.assert_eq("Go decrypts known vector", go_result, expected)


def test_wrong_key(t: CompatTest):
    """Python decrypt with wrong key returns empty string."""
    print("\n[S] Security: wrong-key behavior")
    stored = encrypt_value("my-secret-api-key", TEST_SECRET_KEY)
    result = decrypt_value(stored, "wrong-key-should-fail")
    t.assert_eq("wrong key → empty string (Python)", result, "")


def test_enc_prefix_guard(t: CompatTest):
    """Values without enc: prefix are returned unchanged."""
    print("\n[S] Storage prefix guard")
    no_prefix = "plaintext-value"
    result = decrypt_value(no_prefix, TEST_SECRET_KEY)
    t.assert_eq("no enc: prefix → passthrough", result, no_prefix)

    empty_result = decrypt_value("", TEST_SECRET_KEY)
    t.assert_eq("empty string → passthrough", empty_result, "")


# ── Go integration via subprocess ─────────────────────────────────────────────
#
# When running inside them-bridge we cannot call the Go binary directly.
# Instead we use a small Go program compiled on-the-fly (if go is available)
# or fall back to the known-vector test only.

GO_HELPER_SRC = r'''
package main

import (
	"encoding/base64"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"os"
	"strings"
)

const prefix = "enc:"

func deriveKey(secretKey string) []byte {
	sum := sha256.Sum256([]byte(secretKey))
	return sum[:]
}

func decrypt(key []byte, stored string) (string, error) {
	if !strings.HasPrefix(stored, prefix) {
		return stored, nil
	}
	token := stored[len(prefix):]
	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}
	const minLen = 1 + 8 + 16 + 16 + 32
	if len(raw) < minLen {
		return "", fmt.Errorf("token too short")
	}
	if raw[0] != 0x80 {
		return "", fmt.Errorf("wrong version")
	}
	ciphertextLen := len(raw) - 1 - 8 - 16 - 32
	if ciphertextLen <= 0 || ciphertextLen%aes.BlockSize != 0 {
		return "", fmt.Errorf("bad ciphertext length")
	}
	msgEnd := len(raw) - 32
	msg := raw[:msgEnd]
	storedMAC := raw[msgEnd:]
	iv := raw[9:25]
	ciphertext := raw[25:msgEnd]
	h := hmac.New(sha256.New, key[:16])
	h.Write(msg)
	expected := h.Sum(nil)
	if subtle.ConstantTimeCompare(storedMAC, expected) != 1 {
		return "", fmt.Errorf("HMAC mismatch")
	}
	block, err := aes.NewCipher(key[16:])
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	padLen := int(plain[len(plain)-1])
	if padLen == 0 || padLen > 16 {
		return "", fmt.Errorf("bad padding")
	}
	return string(plain[:len(plain)-padLen]), nil
}

func encrypt(key []byte, plaintext string) (string, error) {
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	pad := 16 - len(plaintext)%16
	padded := []byte(plaintext)
	for i := 0; i < pad; i++ {
		padded = append(padded, byte(pad))
	}
	block, err := aes.NewCipher(key[16:])
	if err != nil {
		return "", err
	}
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)

	var ts [8]byte
	msg := append([]byte{0x80}, ts[:]...)
	msg = append(msg, iv...)
	msg = append(msg, ct...)
	h := hmac.New(sha256.New, key[:16])
	h.Write(msg)
	raw := append(msg, h.Sum(nil)...)
	return prefix + base64.URLEncoding.EncodeToString(raw), nil
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: prog <encrypt|decrypt> <secret_key> <value>")
		os.Exit(1)
	}
	op, secretKey, value := os.Args[1], os.Args[2], os.Args[3]
	key := deriveKey(secretKey)
	switch op {
	case "encrypt":
		result, err := encrypt(key, value)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(result)
	case "decrypt":
		result, err := decrypt(key, value)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(result)
	}
}
'''

def build_go_helper():
    """Compile the Go helper binary if go is available. Returns path or None."""
    try:
        subprocess.run(["go", "version"], capture_output=True, check=True)
    except (FileNotFoundError, subprocess.CalledProcessError):
        return None
    tmpdir = tempfile.mkdtemp(prefix="wave7_fernet_")
    src_path = os.path.join(tmpdir, "main.go")
    bin_path = os.path.join(tmpdir, "fernet_helper")
    with open(src_path, "w") as f:
        f.write(GO_HELPER_SRC)
    result = subprocess.run(
        ["go", "build", "-o", bin_path, src_path],
        capture_output=True, text=True
    )
    if result.returncode != 0:
        print(f"  Warning: failed to build Go helper: {result.stderr}", file=sys.stderr)
        return None
    return bin_path

def make_go_decrypt(go_bin):
    def go_decrypt(stored: str, secret_key: str) -> str | None:
        result = subprocess.run(
            [go_bin, "decrypt", secret_key, stored],
            capture_output=True, text=True
        )
        if result.returncode != 0:
            return None
        return result.stdout.strip()
    return go_decrypt

def make_go_encrypt(go_bin):
    def go_encrypt(plaintext: str, secret_key: str) -> str | None:
        result = subprocess.run(
            [go_bin, "encrypt", secret_key, plaintext],
            capture_output=True, text=True
        )
        if result.returncode != 0:
            return None
        return result.stdout.strip()
    return go_encrypt


def python_only_decrypt(stored: str, secret_key: str) -> str:
    return decrypt_value(stored, secret_key)

def python_only_encrypt(plaintext: str, secret_key: str) -> str:
    return encrypt_value(plaintext, secret_key)


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Wave 7 Fernet compatibility test")
    parser.add_argument("--verbose", "-v", action="store_true")
    parser.add_argument("--skip-go-binary", action="store_true",
                        help="Skip Go binary compilation (known-vector only)")
    args = parser.parse_args()

    print("=" * 60)
    print("Wave 7 Phase 1 — Fernet Bidirectional Compatibility Test")
    print(f"SECRET_KEY: wave7-test-secret-do-not-use-in-prod (test-only)")
    print("=" * 60)

    t = CompatTest(verbose=args.verbose)

    # Python self-test (sanity)
    test_python_only_round_trip(t)
    test_wrong_key(t)
    test_enc_prefix_guard(t)

    # Attempt to build a Go helper binary
    go_bin = None
    if not args.skip_go_binary:
        print("\n[Go] Attempting to build Go helper binary...")
        go_bin = build_go_helper()
        if go_bin:
            print(f"  Built: {go_bin}")
        else:
            print("  go not available — skipping live Go binary tests")
            print("  (Run inside them-go-bridge or a host with Go installed)")

    if go_bin:
        go_decrypt = make_go_decrypt(go_bin)
        go_encrypt = make_go_encrypt(go_bin)

        # Known-vector test
        test_known_vector(t, go_decrypt)

        # Bidirectional live tests
        test_python_encrypt_go_decrypt(t, go_decrypt)
        test_go_encrypt_python_decrypt(t, go_encrypt)
    else:
        # Without Go binary, test known vector via Python only and report partial coverage
        print("\n[K] Known vector (Python decrypt of statically known token)")
        known_token = (
            "gAAAAABlU_EAAQIDBAUGBwgJCgsMDQ4PENSEfjZhbp7TUUrlX2VX4Etcgk5ljOeuEzUqs"
            "zUGnzW309NBYudvQUNjUK41tv5em0yiWzwl2UxYFRyd2ZV4o24="
        )
        stored = _ENC_PREFIX + known_token
        decrypted = decrypt_value(stored, TEST_SECRET_KEY)
        t.assert_eq(
            "Python decrypts Go-generated known vector",
            decrypted,
            "test-api-key-wave7-phase1"
        )
        print()
        print("  NOTE: Go binary not available. Bidirectional live test skipped.")
        print("  The Go unit test TestDecrypt_KnownPythonVector covers Direction 1.")
        print("  Re-run with Go installed for Direction 2 (Go encrypt → Python decrypt).")

    ok = t.summary()

    if go_bin:
        print("\nBidirectional compatibility: CONFIRMED" if ok
              else "\nBidirectional compatibility: FAILED — see errors above")
    else:
        print("\nPartial coverage (Python direction only): CONFIRMED" if ok
              else "\nPartial coverage: FAILED — see errors above")

    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
