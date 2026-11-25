package main

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveRefs(t *testing.T) {
	lookup := func(svc, key string) (string, bool) {
		if svc == "db" && key == "DATABASE_URL" {
			return "postgresql://x@ossr-db:5432/app", true
		}
		return "", false
	}
	out, err := resolveRefs(map[string]string{"DB": "${{db.DATABASE_URL}}", "A": "plain", "B": "x-${{ db.DATABASE_URL }}-y"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if out["DB"] != "postgresql://x@ossr-db:5432/app" || out["A"] != "plain" || out["B"] != "x-postgresql://x@ossr-db:5432/app-y" {
		t.Fatalf("got %v", out)
	}
	if _, err := resolveRefs(map[string]string{"X": "${{nope.KEY}}"}, lookup); err == nil {
		t.Fatal("missing reference should fail")
	}
}

func TestSafeJoin(t *testing.T) {
	base := filepath.FromSlash("/data/builds/1")
	for _, bad := range []string{"..", "../x", "a/../../x", "/../x"} {
		if _, err := safeJoin(base, bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	got, err := safeJoin(base, "apps/web")
	if err != nil || got != filepath.Join(base, "apps", "web") {
		t.Fatalf("got %q %v", got, err)
	}
	if got, _ := safeJoin(base, "/"); got != base {
		t.Fatalf("root should be base, got %q", got)
	}
}

func TestParseDockerStats(t *testing.T) {
	s := parseDockerStats("12.50%", "45MiB / 1.5GiB")
	if s.cpu != 12.5 || s.mem != 45<<20 || s.memLimit != int64(1.5*(1<<30)) {
		t.Fatalf("got %+v", s)
	}
	rx, tx := parsePair("1.2kB / 648B")
	if rx != 1200 || tx != 648 {
		t.Fatalf("got %d %d", rx, tx)
	}
}

func TestWebhookSignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !validSignature("s3cret", body, good) {
		t.Fatal("valid signature rejected")
	}
	if validSignature("other", body, good) || validSignature("s3cret", body, "sha256=00") || validSignature("s3cret", body, "") {
		t.Fatal("invalid signature accepted")
	}
}

func TestSignJWT(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	tok, err := signJWT("123", pemKey, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("bad token %q", tok)
	}
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if !strings.Contains(string(claims), `"iss":"123"`) {
		t.Fatalf("claims %s", claims)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatal("signature does not verify:", err)
	}
	if _, err := signJWT("1", "not a key", time.Now()); err == nil {
		t.Fatal("bad key accepted")
	}
}

func TestCaddyfileRejectsInjection(t *testing.T) {
	conf := caddyfile("", "panel.example.com", "ossr-panel:3000", []route{
		{hosts: []string{"web.example.com", "evil.com {\n respond hi"}, upstream: "ossr-web-1:3000"},
		{hosts: []string{"api.localhost"}, upstream: "ossr-api-2:8080"},
	})
	if strings.Contains(conf, "evil") || strings.Contains(conf, "respond") {
		t.Fatalf("injected host made it into config:\n%s", conf)
	}
	for _, want := range []string{"panel.example.com {", "web.example.com {", "http://api.localhost {", "reverse_proxy ossr-web-1:3000"} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q in:\n%s", want, conf)
		}
	}
}

func TestServiceInputValidate(t *testing.T) {
	ok := []serviceInput{
		{Name: "web", Source: "github", Repo: "acme/web"},
		{Name: "db", Source: "postgres"},
		{Name: "site", Source: "image", Image: "nginx:1.27-alpine"},
		{Name: "blog", Source: "git", Repo: "https://github.com/acme/blog.git"},
	}
	for _, in := range ok {
		if err := in.validate(); err != nil {
			t.Errorf("%+v: %v", in, err)
		}
	}
	bad := []serviceInput{
		{Name: "Web!", Source: "github", Repo: "acme/web"},
		{Name: "caddy", Source: "postgres"},
		{Name: "web", Source: "git", Repo: "http://github.com/x/y"},
		{Name: "web", Source: "git", Repo: "https://user:pw@github.com/x/y"},
		{Name: "web", Source: "github", Repo: "acme/web", Branch: "--upload-pack=x"},
		{Name: "web", Source: "github", Repo: "acme/web", RootDir: "../etc"},
		{Name: "web", Source: "image", Image: "-v /:/host"},
		{Name: "web", Source: "shell"},
	}
	for _, in := range bad {
		if err := in.validate(); err == nil {
			t.Errorf("%+v should be rejected", in)
		}
	}
}

func TestEncryptRoundTrip(t *testing.T) {
	a := &App{key: make([]byte, 32)}
	enc, err := a.encrypt("hello\nworld")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a.decrypt(enc); err != nil || got != "hello\nworld" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestReferencedServices(t *testing.T) {
	got := referencedServices(map[string]string{
		"A": "${{db.DATABASE_URL}}",
		"B": "redis://${{ cache.HOST }}:${{cache.PORT}}",
		"C": "plain",
	})
	if strings.Join(got, ",") != "cache,db" {
		t.Fatalf("got %v", got)
	}
}
