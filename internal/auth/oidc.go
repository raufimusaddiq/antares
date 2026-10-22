// Package auth is Antares' identity layer: Google OIDC login (Authorization
// Code + PKCE), opaque server-side sessions, and the guards that keep a user
// who is not fully eligible away from everything that touches Hermes.
package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Endpoints are the Google OIDC endpoints. Production uses GoogleEndpoints();
// tests substitute a local fake. They are not configurable from the environment.
type Endpoints struct {
	Issuer string   // canonical issuer, stored as users.google_issuer
	Auth   string   // authorization endpoint
	Token  string   // token endpoint
	JWKS   string   // signing keys
	Issued []string // accepted `iss` values
}

func GoogleEndpoints() Endpoints {
	return Endpoints{
		Issuer: "https://accounts.google.com",
		Auth:   "https://accounts.google.com/o/oauth2/v2/auth",
		Token:  "https://oauth2.googleapis.com/token",
		JWKS:   "https://www.googleapis.com/oauth2/v3/certs",
		Issued: []string{"https://accounts.google.com", "accounts.google.com"},
	}
}

// Claims are the verified identity facts taken from an ID token.
type Claims struct {
	Subject string
	Email   string
	Name    string
	Picture string
}

// errors are fixed strings: they are recorded in the audit log and must never
// carry token or secret content.
var (
	ErrToken = errors.New("id token invalid")
)

const (
	clockSkew     = 60 * time.Second
	jwksTTL       = time.Hour
	jwksMinRetry  = time.Minute
	maxBody       = 1 << 20
	exchangeLimit = 10 * time.Second
)

// verifier validates ID tokens against Google's published keys.
type verifier struct {
	ep       Endpoints
	clientID string
	client   *http.Client
	now      func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	lastTry   time.Time
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type jwtClaims struct {
	Iss           string          `json:"iss"`
	Aud           json.RawMessage `json:"aud"`
	Azp           string          `json:"azp"`
	Sub           string          `json:"sub"`
	Exp           *json.Number    `json:"exp"`
	Iat           *json.Number    `json:"iat"`
	Nonce         string          `json:"nonce"`
	Email         string          `json:"email"`
	EmailVerified json.RawMessage `json:"email_verified"`
	Name          string          `json:"name"`
	Picture       string          `json:"picture"`
}

// verify checks signature (RS256 only), iss, aud, exp, nonce and email_verified.
// Any failure is ErrToken: a token that cannot be validated is a failed login.
func (v *verifier) verify(ctx context.Context, raw, wantNonce string) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Claims{}, ErrToken
	}
	var h jwtHeader
	if !decodeSegment(parts[0], &h) || h.Alg != "RS256" || h.Kid == "" {
		return Claims{}, ErrToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrToken
	}
	key, err := v.key(ctx, h.Kid)
	if err != nil {
		return Claims{}, ErrToken
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return Claims{}, ErrToken
	}

	var c jwtClaims
	if !decodeSegment(parts[1], &c) {
		return Claims{}, ErrToken
	}
	issOK := false
	for _, iss := range v.ep.Issued {
		issOK = issOK || c.Iss == iss
	}
	if !issOK || !v.audienceOK(c) {
		return Claims{}, ErrToken
	}
	now := v.now()
	exp, ok := unixTime(c.Exp)
	if !ok || !now.Before(exp.Add(clockSkew)) {
		return Claims{}, ErrToken
	}
	if iat, ok := unixTime(c.Iat); !ok || iat.After(now.Add(clockSkew)) {
		return Claims{}, ErrToken
	}
	if wantNonce == "" || subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(wantNonce)) != 1 {
		return Claims{}, ErrToken
	}
	if c.Sub == "" || len(c.Sub) > 255 || c.Email == "" || !emailVerified(c.EmailVerified) {
		return Claims{}, ErrToken
	}
	return Claims{Subject: c.Sub, Email: c.Email, Name: c.Name, Picture: c.Picture}, nil
}

func (v *verifier) audienceOK(c jwtClaims) bool {
	var one string
	if json.Unmarshal(c.Aud, &one) == nil {
		return one == v.clientID
	}
	var many []string
	if json.Unmarshal(c.Aud, &many) != nil {
		return false
	}
	for _, a := range many {
		if a == v.clientID {
			// OIDC core 3.1.3.7: with several audiences, azp must name us.
			return len(many) == 1 || c.Azp == v.clientID
		}
	}
	return false
}

func emailVerified(raw json.RawMessage) bool {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	var s string // Google has historically emitted the string form
	return json.Unmarshal(raw, &s) == nil && s == "true"
}

func unixTime(n *json.Number) (time.Time, bool) {
	if n == nil {
		return time.Time{}, false
	}
	f, err := n.Float64()
	if err != nil || f <= 0 || f > 1e11 {
		return time.Time{}, false
	}
	return time.Unix(int64(f), 0), true
}

func decodeSegment(seg string, out any) bool {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	return err == nil && json.Unmarshal(b, out) == nil
}

// key returns the signing key for kid, refreshing the JWKS when it is stale or
// the kid is unknown (key rotation). Refreshes are rate-limited so a stream of
// forged kids cannot turn into a stream of outbound requests.
func (v *verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	if k, ok := v.keys[kid]; ok && now.Sub(v.fetchedAt) < jwksTTL {
		return k, nil
	}
	if v.keys != nil && now.Sub(v.lastTry) < jwksMinRetry {
		if k, ok := v.keys[kid]; ok {
			return k, nil
		}
		return nil, errors.New("unknown key id")
	}
	v.lastTry = now
	keys, err := v.fetchKeys(ctx)
	if err != nil {
		return nil, err
	}
	v.keys, v.fetchedAt = keys, now
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, errors.New("unknown key id")
}

func (v *verifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	ctx, cancel := context.WithTimeout(ctx, exchangeLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.ep.JWKS, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, errors.New("jwks fetch failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks status %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kty, Kid, Alg, Use, N, E string
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&set); err != nil {
		return nil, errors.New("jwks decode failed")
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, e := b64Int(k.N), b64Int(k.E)
		if n == nil || e == nil || !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31-1 || n.BitLen() < 2048 {
			continue
		}
		out[k.Kid] = &rsa.PublicKey{N: n, E: int(e.Int64())}
	}
	if len(out) == 0 {
		return nil, errors.New("jwks holds no usable key")
	}
	return out, nil
}

func b64Int(s string) *big.Int {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) == 0 {
		return nil
	}
	return new(big.Int).SetBytes(b)
}

// exchange redeems the authorization code (with the PKCE verifier) for an ID
// token. The access token in the response is discarded unread: Antares uses
// Google only to establish who the user is (PRD §4.2).
func exchange(ctx context.Context, client *http.Client, ep Endpoints, clientID, clientSecret, redirectURI, code, verifier string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, exchangeLimit)
	defer cancel()
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("token exchange failed") // the URL error would be fine, but never risk echoing the form
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange status %d", resp.StatusCode)
	}
	var out struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil || out.IDToken == "" {
		return "", errors.New("token exchange returned no id_token")
	}
	return out.IDToken, nil
}

// pkceChallenge is the S256 code challenge for a verifier.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
