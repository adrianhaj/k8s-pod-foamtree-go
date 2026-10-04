package llm

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	jwt := "eyJhbGciOiJSUzI1NiIsImtpZCI6IjEifQ.eyJzdWIiOiJzeXN0ZW06c2VydmljZWFjY291bnQifQ.c2lnbmF0dXJlLXNpZ25hdHVyZQ"
	for in, want := range map[string]string{
		"DB_PASSWORD=hunter2hunter2":                                             "DB_PASSWORD=[REDACTED]",
		`{"apiKey": "abcd1234efgh"}`:                                             `{"apiKey": "[REDACTED]"}`,
		"client_secret: abc123xyz":                                               "client_secret: [REDACTED]",
		"connect postgres://app:s3cretpw@db:5432/payments":                       "connect postgres://[REDACTED]@db:5432/payments",
		"token=" + jwt:                                                           "token=[REDACTED]",
		"sa token " + jwt + " loaded":                                            "sa token [REDACTED] loaded",
		"key AKIAIOSFODNN7EXAMPLE used":                                          "key [REDACTED] used",
		"ghp_" + strings.Repeat("a", 36):                                         "[REDACTED]",
		"slack xoxb-1234567890-abcdefghij":                                       "slack [REDACTED]",
		"OPENAI sk-proj-" + strings.Repeat("b", 30):                              "OPENAI [REDACTED]",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----": "[REDACTED]",
		// Ordinary log lines stay as they are.
		"password reset email sent to alice@example.com":                              "password reset email sent to alice@example.com",
		"token bucket refilled: 40 tokens":                                            "token bucket refilled: 40 tokens",
		"GET /healthz 200 in 3ms":                                                     "GET /healthz 200 in 3ms",
		"uuid 123e4567-e89b-12d3-a456-426614174000":                                   "uuid 123e4567-e89b-12d3-a456-426614174000",
		"runtime: out of memory: cannot allocate 33554432-byte block":                 "runtime: out of memory: cannot allocate 33554432-byte block",
		"image sha256:9b2a7f0c1d2e3f4a5b6c7d8e9f00112233445566778899aabbccddeeff0011": "image sha256:9b2a7f0c1d2e3f4a5b6c7d8e9f00112233445566778899aabbccddeeff0011",
	} {
		if got, _ := redact(in); got != want {
			t.Errorf("%q\n got %q\nwant %q", in, got, want)
		}
	}
	if got, n := redact("Authorization: Bearer " + jwt); strings.Contains(got, "eyJ") || n < 1 {
		t.Errorf("bearer: %q %d", got, n)
	}
	if _, n := redact("a=1 password=hunter22 x postgres://u:p4ss@h/db"); n != 2 {
		t.Errorf("count %d", n)
	}
	if again, n := redact("password=[REDACTED]"); again != "password=[REDACTED]" || n != 0 {
		t.Errorf("not idempotent: %q %d", again, n)
	}
}
