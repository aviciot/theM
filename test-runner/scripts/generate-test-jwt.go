package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Generate a valid HS256 JWT for testing
// Usage: go run generate-test-jwt.go <secret> <user_id>
// Example: go run generate-test-jwt.go "your-secret-key" 1

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run generate-test-jwt.go <secret> <user_id>")
		os.Exit(1)
	}

	secret := os.Args[1]
	userID := os.Args[2]

	// Header
	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}
	headerJSON, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	// Payload
	now := time.Now().Unix()
	payload := map[string]interface{}{
		"sub":      userID,
		"user_id":  userID,
		"username": "test-viewer",
		"email":    "test-viewer@example.com",
		"iat":      now,
		"exp":      now + 3600*24*365, // 1 year
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// Signature
	message := headerB64 + "." + payloadB64
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	// JWT
	jwt := message + "." + signature
	fmt.Println(jwt)
}
