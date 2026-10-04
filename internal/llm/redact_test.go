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

func TestRedactKeepsProse(t *testing.T) {
	for _, in := range []string{
		"failed to authorize: failed to fetch oauth token: unexpected status: 401 Unauthorized",
		"author=alice authorized=true",
		"authority=https://login.microsoftonline.com/x",
		"authentication=enabled",
		"auth_method=oidc",
		"passwordless=true",
		"tokens_used: 1234",
		"token_count=4096",
		"max_tokens=2048 total_tokens: 3000",
		"tokenizer=cl100k_base",
		"credentials: loaded from /var/run/secrets",
		"secrets: synced 4 items",
		"secretName: my-tls",
		"bearer authentication failed",
		"Basic Authentication failed",
		"password=[REDACTED]",
		"password=true",
		"password: null",
		`secret: ""`,
		"postgres://[REDACTED]:[REDACTED]@h/db",
	} {
		if got, n := redact(in); got != in || n != 0 {
			t.Errorf("%q\n got %q (%d)", in, got, n)
		}
	}
}

func TestRedactLeaks(t *testing.T) {
	for in, secret := range map[string]string{
		`{"level":"debug","body":"{\"username\":\"alice\",\"password\":\"hunter2hunter2\"}"}`: "hunter2hunter2",
		`msg="request body: {\"api_key\": \"abcd1234efgh5678\"}"`:                             "abcd1234efgh5678",
		"Authorization: token ghx0123456789abcdefsecret":                                      "ghx0123456789abcdefsecret",
		"Authorization: Token abc12345opaque":                                                 "abc12345opaque",
		"authorization: ApiKey abc12345opaque":                                                "abc12345opaque",
		"Authorization: AWS4-HMAC-SHA256 Credential=AKID/20261004/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=fe5f80f77d5fa3beca038a248ff027d0445342fe2855ddc963176630326f1024": "fe5f80f77d5fa3beca038a248ff027d0445342fe2855ddc963176630326f1024",
		"redis://:s3cretpw@redis:6379/0":                  "s3cretpw",
		`password: "correct horse battery staple"`:        "battery",
		`password: 'hunter2 with spaces'`:                 "spaces",
		"run --password hunter2hunter2":                   "hunter2hunter2",
		"run --password=hunter2hunter2":                   "hunter2hunter2",
		"run --token abcdef0123456789":                    "abcdef0123456789",
		"run --api-key abcd1234efgh":                      "abcd1234efgh",
		"run --secret xyz12345":                           "xyz12345",
		"PGPASSWORD=hunter2hunter2":                       "hunter2hunter2",
		"DB_PASS=hunter2hunter2":                          "hunter2hunter2",
		"AccountKey=Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MA==;x=1": "Zm9vYmFy",
		"https://s.blob/x?sv=1&sig=AbCdEf0123456789%3D":   "AbCdEf0123456789",
		"X-Amz-Signature=fe5f80f77d5f0123456789abcdef":    "fe5f80f77d5f",
		"glpat-abcdefghij0123456789":                      "abcdefghij0123456789",
		"hf_abcdefghijklmnopqrstuvwxyz0123456789":         "abcdefghijklmnopqrstuvwxyz",
		"npm_abcdefghijklmnopqrstuvwxyz0123456789":        "abcdefghijklmnopqrstuvwxyz",
		"password=hunter":                                 "hunter",
		"password=12345678":                               "12345678",
		`pwd: "abcd"`:                                     "abcd",
		"client_secret=abcdefghij":                        "abcdefghij",
	} {
		if got, n := redact(in); strings.Contains(got, secret) || n < 1 {
			t.Errorf("%q\n got %q (%d)", in, got, n)
		}
	}
	jwt := "eyJhbGciOiJSUzI1NiIsImtpZCI6IjEifQ.eyJzdWIiOiJzeXN0ZW06c2VydmljZWFjY291bnQifQ.c2lnbmF0dXJlLXNpZ25hdHVyZQ"
	for _, in := range []string{"Authorization: Bearer " + jwt, "Authorization: Basic dXNlcjpwYXNz"} {
		if got, n := redact(in); got != "Authorization: [REDACTED]" || n != 1 {
			t.Errorf("%q: %q %d", in, got, n)
		}
	}
}
