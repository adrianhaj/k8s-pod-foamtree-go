package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// fakeIdP is a minimal OIDC provider: discovery, JWKS, and a token endpoint
// that checks PKCE and signs whatever claims the test sets.
type fakeIdP struct {
	*httptest.Server
	key       *rsa.PrivateKey
	claims    map[string]any
	nonce     string
	challenge string
}

func newIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &fakeIdP{key: key}
	mux := http.NewServeMux()
	idp.Server = httptest.NewServer(mux)
	t.Cleanup(idp.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": idp.URL, "authorization_endpoint": idp.URL + "/authorize", "token_endpoint": idp.URL + "/token",
			"jwks_uri": idp.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		sum := sha256.Sum256([]byte(r.FormValue("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != idp.challenge {
			http.Error(w, "pkce mismatch", http.StatusBadRequest)
			return
		}
		claims := map[string]any{"iss": idp.URL, "aud": "dash", "sub": "u1", "nonce": idp.nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
		for k, v := range idp.claims {
			claims[k] = v
		}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k"))
		payload, _ := json.Marshal(claims)
		jws, _ := signer.Sign(payload)
		raw, _ := jws.CompactSerialize()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "token_type": "Bearer", "id_token": raw})
	})
	return idp
}

func newAuth(t *testing.T, idp *fakeIdP, emails, groups []string) *Auth {
	t.Helper()
	a, err := New(context.Background(), Config{
		Issuer: idp.URL, ClientID: "dash", RedirectURL: "http://dash.test/auth/callback",
		Scopes: []string{"openid", "email"}, GroupsClaim: "groups",
		AllowedEmails: emails, AllowedGroups: groups, SessionKey: make([]byte, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func handler(a *Auth) http.Handler {
	mux := http.NewServeMux()
	a.Register(mux)
	mux.Handle("/", a.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		json.NewEncoder(w).Encode(u)
	})))
	return mux
}

func do(h http.Handler, method, target string, cookies []*http.Cookie, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Login cookies are named per state, so name also matches "<name>_<state>".
func cookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name || strings.HasPrefix(c.Name, name+"_") {
			return c
		}
	}
	return nil
}

// login walks /auth/login → IdP → /auth/callback and returns the callback response.
func login(t *testing.T, h http.Handler, idp *fakeIdP, claims map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return loginFrom(t, h, idp, claims, "/auth/login")
}

func loginFrom(t *testing.T, h http.Handler, idp *fakeIdP, claims map[string]any, start string) *httptest.ResponseRecorder {
	t.Helper()
	callback, jar := startLogin(t, h, idp, start)
	idp.claims = claims
	return do(h, "GET", callback, jar)
}

// startLogin returns the callback URL the IdP would send the browser to, and
// the cookies the browser would hold for it.
func startLogin(t *testing.T, h http.Handler, idp *fakeIdP, start string) (string, []*http.Cookie) {
	t.Helper()
	w := do(h, "GET", start, nil)
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil || w.Code != http.StatusFound {
		t.Fatalf("login redirect: %d %v", w.Code, err)
	}
	q := loc.Query()
	if q.Get("code_challenge_method") != "S256" {
		t.Fatal("login must use PKCE S256")
	}
	idp.nonce, idp.challenge = q.Get("nonce"), q.Get("code_challenge")
	return "/auth/callback?code=c&state=" + q.Get("state"), w.Result().Cookies()
}

func TestAnonymousIsRedirectedOrRejected(t *testing.T) {
	h := handler(newAuth(t, newIdP(t), []string{"*@example.com"}, nil))
	if w := do(h, "GET", "/?context=prod", nil, "Accept", "text/html"); w.Code != http.StatusFound || w.Header().Get("Location") != "/auth/login?next=%2F%3Fcontext%3Dprod" {
		t.Fatalf("page: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := do(h, "GET", "/resources/cpu", nil, "Accept", "*/*"); w.Code != http.StatusUnauthorized {
		t.Fatalf("api: %d", w.Code)
	}
}

func TestLoginGrantsSession(t *testing.T) {
	idp := newIdP(t)
	h := handler(newAuth(t, idp, []string{"*@example.com"}, nil))
	w := login(t, h, idp, map[string]any{"email": "Ada@Example.com", "email_verified": true, "name": "Ada"})
	s := cookie(w, sessionCookie)
	if w.Code != http.StatusFound || s == nil || !s.HttpOnly || s.SameSite != http.SameSiteLaxMode {
		t.Fatalf("callback: %d %s cookie=%+v", w.Code, w.Body, s)
	}
	me := do(h, "GET", "/api/me", []*http.Cookie{s})
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"email":"Ada@Example.com"`) {
		t.Fatalf("me: %d %s", me.Code, me.Body)
	}
}

func TestGroupGrantsSession(t *testing.T) {
	idp := newIdP(t)
	h := handler(newAuth(t, idp, nil, []string{"sre"}))
	if w := login(t, h, idp, map[string]any{"email": "bob@other.org", "groups": []string{"dev", "sre"}}); cookie(w, sessionCookie) == nil {
		t.Fatalf("group member denied: %d %s", w.Code, w.Body)
	}
}

func TestDenied(t *testing.T) {
	cases := map[string]map[string]any{
		"not on the list":   {"email": "eve@evil.com", "email_verified": true},
		"unverified email":  {"email": "eve@example.com", "email_verified": false},
		"no email_verified": {"email": "eve@example.com"},
		"xms_edov false":    {"email": "eve@example.com", "xms_edov": false},
		"wrong group":       {"email": "eve@evil.com", "groups": []string{"dev"}},
		"groups not list":   {"email": "eve@evil.com", "groups": "sre"},
	}
	for name, claims := range cases {
		t.Run(name, func(t *testing.T) {
			idp := newIdP(t)
			h := handler(newAuth(t, idp, []string{"*@example.com"}, []string{"sre"}))
			w := login(t, h, idp, claims)
			if w.Code != http.StatusForbidden || cookie(w, sessionCookie) != nil {
				t.Fatalf("got %d, cookie=%v", w.Code, cookie(w, sessionCookie))
			}
		})
	}
}

func TestLoginReturnsToRequestedPage(t *testing.T) {
	ada := map[string]any{"email": "ada@example.com", "email_verified": true}
	cases := map[string]string{
		"/auth/login?next=%2F%3Fcontext%3Dprod": "/?context=prod",
		"/auth/login":                           "/",
		"/auth/login?next=https://evil.com":     "/",
		"/auth/login?next=//evil.com":           "/",
		`/auth/login?next=/\evil.com`:           "/",
		"/auth/login?next=javascript:alert(1)":  "/",
	}
	for start, want := range cases {
		t.Run(start, func(t *testing.T) {
			idp := newIdP(t)
			h := handler(newAuth(t, idp, []string{"*@example.com"}, nil))
			if w := loginFrom(t, h, idp, ada, start); w.Header().Get("Location") != want {
				t.Fatalf("got %d %q, want %q", w.Code, w.Header().Get("Location"), want)
			}
		})
	}
}

// Two signed-out tabs both start a login; the browser holds both login
// cookies, and whichever tab returns first must still succeed.
func TestConcurrentLogins(t *testing.T) {
	idp := newIdP(t)
	h := handler(newAuth(t, idp, []string{"*@example.com"}, nil))
	first, jar1 := startLogin(t, h, idp, "/auth/login")
	nonce1, challenge1 := idp.nonce, idp.challenge
	second, jar2 := startLogin(t, h, idp, "/auth/login")
	jar := map[string]*http.Cookie{}
	for _, c := range append(jar1, jar2...) {
		jar[c.Name] = c
	}
	var cookies []*http.Cookie
	for _, c := range jar {
		cookies = append(cookies, c)
	}
	idp.claims = map[string]any{"email": "ada@example.com", "email_verified": true}
	if w := do(h, "GET", second, cookies); cookie(w, sessionCookie) == nil {
		t.Fatalf("second tab: %d %s", w.Code, w.Body)
	}
	idp.nonce, idp.challenge = nonce1, challenge1
	if w := do(h, "GET", first, cookies); cookie(w, sessionCookie) == nil {
		t.Fatalf("first tab: %d %s", w.Code, w.Body)
	}
}

func TestCallbackRejectsForgedState(t *testing.T) {
	idp := newIdP(t)
	h := handler(newAuth(t, idp, []string{"*@example.com"}, nil))
	start := do(h, "GET", "/auth/login", nil)
	if w := do(h, "GET", "/auth/callback?code=c&state=forged", []*http.Cookie{cookie(start, loginCookie)}); w.Code != http.StatusBadRequest {
		t.Fatalf("got %d", w.Code)
	}
	if w := do(h, "GET", "/auth/callback?code=c&state=x", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("no login cookie: got %d", w.Code)
	}
}

func TestForgedSessionsAreRejected(t *testing.T) {
	idp := newIdP(t)
	a := newAuth(t, idp, []string{"*@example.com"}, nil)
	h := handler(a)
	loginValue := cookie(do(h, "GET", "/auth/login", nil), loginCookie).Value
	expired := a.seal(sessionCookie, User{Email: "a@example.com"}, -time.Minute)
	good := a.seal(sessionCookie, User{Email: "a@example.com"}, time.Minute)
	tampered := good[:len(good)-2] + "AA"
	for name, v := range map[string]string{"login cookie replayed": loginValue, "expired": expired, "tampered": tampered, "garbage": "x"} {
		if w := do(h, "GET", "/api/me", []*http.Cookie{{Name: sessionCookie, Value: v}}); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d", name, w.Code)
		}
	}
}

func TestLogoutDeletesSession(t *testing.T) {
	h := handler(newAuth(t, newIdP(t), []string{"*@example.com"}, nil))
	w := do(h, "POST", "/auth/logout", nil)
	if w.Code != http.StatusOK || w.Header().Get("Location") != "" {
		t.Fatalf("logout: %d location=%q", w.Code, w.Header().Get("Location"))
	}
	if !strings.Contains(w.Body.String(), `href="/auth/login"`) {
		t.Fatalf("logout page must link to /auth/login: %s", w.Body)
	}
	c := cookie(w, sessionCookie)
	if c == nil || c.Path != "/" || c.MaxAge >= 0 {
		t.Fatalf("session cookie not deleted: %+v", c)
	}
	if w := do(h, "GET", "/auth/logout", nil); cookie(w, sessionCookie) != nil {
		t.Fatal("GET logout must not work: it is CSRF-able")
	}
	if w := do(h, "POST", "/auth/logout", nil, "Sec-Fetch-Site", "cross-site"); cookie(w, sessionCookie) != nil {
		t.Fatal("cross-site POST logout must be rejected")
	}
}

func TestStringEmailVerified(t *testing.T) {
	idp := newIdP(t)
	h := handler(newAuth(t, idp, []string{"*@example.com"}, nil))
	if w := login(t, h, idp, map[string]any{"email": "ada@example.com", "email_verified": "true"}); cookie(w, sessionCookie) == nil {
		t.Fatalf("string email_verified=true denied: %d %s", w.Code, w.Body)
	}
	if w := login(t, h, idp, map[string]any{"email": "eve@example.com", "email_verified": "false"}); w.Code != http.StatusForbidden {
		t.Fatalf("string email_verified=false: got %d", w.Code)
	}
}

func TestEntraDomainVerifiedEmail(t *testing.T) {
	idp := newIdP(t)
	h := handler(newAuth(t, idp, []string{"*@example.com"}, nil))
	if w := login(t, h, idp, map[string]any{"email": "ada@example.com", "xms_edov": true}); cookie(w, sessionCookie) == nil {
		t.Fatalf("xms_edov=true denied: %d %s", w.Code, w.Body)
	}
}

func TestNewRefusesOpenAllowlist(t *testing.T) {
	_, err := New(context.Background(), Config{Issuer: "https://idp", ClientID: "c", RedirectURL: "https://d/cb", SessionKey: make([]byte, 32)})
	if err == nil || !strings.Contains(err.Error(), "allowed") {
		t.Fatalf("got %v", err)
	}
}

func TestNewRejectsBadAllowlistGlob(t *testing.T) {
	_, err := New(context.Background(), Config{
		Issuer: "https://idp", ClientID: "c", RedirectURL: "https://d/cb",
		AllowedEmails: []string{"[bad"}, SessionKey: make([]byte, 32),
	})
	if err == nil || !errors.Is(err, path.ErrBadPattern) {
		t.Fatalf("got %v", err)
	}
}

func TestSecureCookieOnHTTPSRedirect(t *testing.T) {
	idp := newIdP(t)
	a, err := New(context.Background(), Config{
		Issuer: idp.URL, ClientID: "dash", RedirectURL: "https://dash.test/auth/callback",
		Scopes: []string{"openid", "email"}, AllowedEmails: []string{"*@example.com"}, SessionKey: make([]byte, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := handler(a)
	start := do(h, "GET", "/auth/login", nil)
	if c := cookie(start, loginCookie); c == nil || !c.Secure {
		t.Fatalf("login cookie not secure: %+v", c)
	}
	w := login(t, h, idp, map[string]any{"email": "ada@example.com", "email_verified": true})
	if c := cookie(w, sessionCookie); c == nil || !c.Secure {
		t.Fatalf("session cookie not secure: %+v", c)
	}
}
