package auth

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Authenticator struct {
	Username string
	Password string
	Realm    string
	TTL      time.Duration
	secret   []byte

	mu     sync.Mutex
	replay map[string]time.Time
}

func New(username, password, realm string) *Authenticator {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return &Authenticator{
		Username: username,
		Password: password,
		Realm:    realm,
		TTL:      5 * time.Minute,
		secret:   secret,
		replay:   make(map[string]time.Time),
	}
}

func (a *Authenticator) Challenge(w http.ResponseWriter, stale bool) {
	nonce := a.newNonce(time.Now().UTC())
	value := fmt.Sprintf(`Digest realm=%q, nonce=%q, algorithm=MD5, qop=%q`, a.Realm, nonce, "auth")
	if stale {
		value += `, stale=true`
	}
	w.Header().Set("WWW-Authenticate", value)
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

func (a *Authenticator) VerifyHTTP(r *http.Request) (ok bool, stale bool) {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(h), "digest ") {
		return false, false
	}
	p := parseDigest(h[len("Digest "):])
	if p["username"] != a.Username || p["realm"] != a.Realm || p["nonce"] == "" || p["uri"] == "" || p["response"] == "" {
		return false, false
	}
	if !a.validNonce(p["nonce"], time.Now().UTC()) {
		return false, true
	}
	reqURI := r.URL.RequestURI()
	if p["uri"] != reqURI && p["uri"] != r.URL.Path {
		return false, false
	}
	ha1 := md5hex(a.Username + ":" + a.Realm + ":" + a.Password)
	ha2 := md5hex(r.Method + ":" + p["uri"])
	var expected string
	if p["qop"] != "" {
		if p["qop"] != "auth" || p["nc"] == "" || p["cnonce"] == "" {
			return false, false
		}
		expected = md5hex(ha1 + ":" + p["nonce"] + ":" + p["nc"] + ":" + p["cnonce"] + ":" + p["qop"] + ":" + ha2)
	} else {
		expected = md5hex(ha1 + ":" + p["nonce"] + ":" + ha2)
	}
	return secureEqual(expected, p["response"]), false
}

func (a *Authenticator) VerifyWSSE(body []byte, now time.Time) bool {
	token, err := parseUsernameToken(body)
	if err != nil || token.Username != a.Username || token.Password == "" || token.Nonce == "" || token.Created == "" {
		return false
	}
	if token.PasswordType != "" && !strings.Contains(token.PasswordType, "PasswordDigest") {
		return false
	}
	created, err := time.Parse(time.RFC3339Nano, token.Created)
	if err != nil {
		return false
	}
	if d := now.UTC().Sub(created.UTC()); d > a.TTL || d < -a.TTL {
		return false
	}
	nonce, err := base64.StdEncoding.DecodeString(token.Nonce)
	if err != nil {
		return false
	}
	h := sha1.New()
	_, _ = h.Write(nonce)
	_, _ = h.Write([]byte(token.Created))
	_, _ = h.Write([]byte(a.Password))
	expected := base64.StdEncoding.EncodeToString(h.Sum(nil))
	if !secureEqual(expected, token.Password) {
		return false
	}
	replayKey := token.Username + "\x00" + token.Nonce + "\x00" + token.Created
	return a.acceptReplayKey(replayKey, now.UTC())
}

func (a *Authenticator) newNonce(now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(ts))
	payload := ts + ":" + hex.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func (a *Authenticator) validNonce(nonce string, now time.Time) bool {
	raw, err := base64.RawURLEncoding.DecodeString(nonce)
	if err != nil {
		return false
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 2 {
		return false
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false
	}
	t := time.Unix(ts, 0).UTC()
	if now.Sub(t) > a.TTL || t.Sub(now) > a.TTL {
		return false
	}
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	return secureEqual(expected, parts[1])
}

func (a *Authenticator) acceptReplayKey(key string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, expiry := range a.replay {
		if now.After(expiry) {
			delete(a.replay, k)
		}
	}
	if _, exists := a.replay[key]; exists {
		return false
	}
	a.replay[key] = now.Add(a.TTL)
	return true
}

func md5hex(v string) string {
	sum := md5.Sum([]byte(v))
	return hex.EncodeToString(sum[:])
}

func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func parseDigest(v string) map[string]string {
	out := make(map[string]string)
	for len(v) > 0 {
		v = strings.TrimLeft(v, " ,\t")
		if v == "" {
			break
		}
		eq := strings.IndexByte(v, '=')
		if eq <= 0 {
			break
		}
		key := strings.TrimSpace(v[:eq])
		v = strings.TrimSpace(v[eq+1:])
		var value string
		if strings.HasPrefix(v, `"`) {
			v = v[1:]
			var b strings.Builder
			escaped := false
			i := 0
			for ; i < len(v); i++ {
				c := v[i]
				if escaped {
					b.WriteByte(c)
					escaped = false
					continue
				}
				if c == '\\' {
					escaped = true
					continue
				}
				if c == '"' {
					i++
					break
				}
				b.WriteByte(c)
			}
			value = b.String()
			v = v[i:]
		} else {
			comma := strings.IndexByte(v, ',')
			if comma < 0 {
				value, v = strings.TrimSpace(v), ""
			} else {
				value, v = strings.TrimSpace(v[:comma]), v[comma+1:]
			}
		}
		out[key] = value
	}
	return out
}

type usernameToken struct {
	Username     string
	Password     string
	PasswordType string
	Nonce        string
	Created      string
}

func parseUsernameToken(body []byte) (usernameToken, error) {
	dec := xml.NewDecoder(strings.NewReader(string(body)))
	var tok usernameToken
	inToken := false
	for {
		t, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return tok, err
		}
		switch se := t.(type) {
		case xml.StartElement:
			if se.Name.Local == "UsernameToken" {
				inToken = true
				continue
			}
			if !inToken {
				continue
			}
			var text string
			switch se.Name.Local {
			case "Username", "Password", "Nonce", "Created":
				if err := dec.DecodeElement(&text, &se); err != nil {
					return tok, err
				}
				switch se.Name.Local {
				case "Username":
					tok.Username = text
				case "Password":
					tok.Password = text
					for _, attr := range se.Attr {
						if attr.Name.Local == "Type" {
							tok.PasswordType = attr.Value
						}
					}
				case "Nonce":
					tok.Nonce = text
				case "Created":
					tok.Created = text
				}
			}
		case xml.EndElement:
			if se.Name.Local == "UsernameToken" {
				return tok, nil
			}
		}
	}
	return tok, errors.New("UsernameToken not found")
}
