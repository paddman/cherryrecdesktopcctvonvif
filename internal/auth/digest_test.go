package auth

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
)

func TestHTTPDigest(t *testing.T) {
	a := New("admin", "a-strong-password", "Cherry")
	rr := httptest.NewRecorder()
	a.Challenge(rr, false)
	nonce := regexp.MustCompile(`nonce="([^"]+)"`).FindStringSubmatch(rr.Header().Get("WWW-Authenticate"))[1]
	uri := "/onvif/device_service"
	nc, cnonce := "00000001", "abcdef"
	ha1 := hash("admin:Cherry:a-strong-password")
	ha2 := hash("POST:" + uri)
	response := hash(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":auth:" + ha2)
	req := httptest.NewRequest("POST", uri, nil)
	req.Header.Set("Authorization", fmt.Sprintf(`Digest username="admin", realm="Cherry", nonce="%s", uri="%s", response="%s", qop=auth, nc=%s, cnonce="%s"`, nonce, uri, response, nc, cnonce))
	ok, stale := a.VerifyHTTP(req)
	if !ok || stale {
		t.Fatalf("digest failed: ok=%v stale=%v", ok, stale)
	}
}

func TestWSSEPasswordDigestAndReplay(t *testing.T) {
	a := New("admin", "a-strong-password", "Cherry")
	now := time.Now().UTC().Truncate(time.Second)
	nonceRaw := []byte("0123456789abcdef")
	nonce := base64.StdEncoding.EncodeToString(nonceRaw)
	h := sha1Digest(append(append(append([]byte{}, nonceRaw...), []byte(now.Format(time.RFC3339))...), []byte("a-strong-password")...))
	password := base64.StdEncoding.EncodeToString(h)
	body := []byte(fmt.Sprintf(`<s:Envelope><s:Header><wsse:Security><wsse:UsernameToken><wsse:Username>admin</wsse:Username><wsse:Password Type="PasswordDigest">%s</wsse:Password><wsse:Nonce>%s</wsse:Nonce><wsu:Created>%s</wsu:Created></wsse:UsernameToken></wsse:Security></s:Header></s:Envelope>`, password, nonce, now.Format(time.RFC3339)))
	if !a.VerifyWSSE(body, now) {
		t.Fatal("expected WSSE token to validate")
	}
	if a.VerifyWSSE(body, now) {
		t.Fatal("replay must be rejected")
	}
}

func sha1Digest(b []byte) []byte { v := sha1.Sum(b); return v[:] }

func hash(s string) string {
	v := md5.Sum([]byte(s))
	return hex.EncodeToString(v[:])
}
