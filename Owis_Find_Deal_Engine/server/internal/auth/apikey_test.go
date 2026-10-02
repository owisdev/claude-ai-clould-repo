package auth

import (
	"context"
	"strings"
	"testing"
)

func TestKeyStore(t *testing.T) {
	key, hash, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, KeyPrefix) {
		t.Errorf("key %q lacks prefix", key)
	}

	ks, err := ParseKeyStore(" mobile:" + hash + " , ")
	if err != nil {
		t.Fatalf("ParseKeyStore: %v", err)
	}
	if c, ok := ks.Lookup(key); !ok || c.Name != "mobile" {
		t.Errorf("Lookup(valid) = %v, %v", c, ok)
	}
	if _, ok := ks.Lookup(key + "x"); ok {
		t.Error("Lookup(wrong key) succeeded")
	}
	if _, ok := ks.Lookup(""); ok {
		t.Error("Lookup(empty) succeeded")
	}
}

func TestParseKeyStoreErrors(t *testing.T) {
	for _, spec := range []string{"", "nohash", "name:zz", ":" + HashKey("x"), "a:abcd"} {
		if _, err := ParseKeyStore(spec); err == nil {
			t.Errorf("ParseKeyStore(%q): expected error", spec)
		}
	}
}

func TestClientContext(t *testing.T) {
	ctx := WithClient(context.Background(), Client{Name: "web"})
	if c, ok := ClientFrom(ctx); !ok || c.Name != "web" {
		t.Errorf("ClientFrom = %v, %v", c, ok)
	}
	if _, ok := ClientFrom(context.Background()); ok {
		t.Error("ClientFrom(empty ctx) succeeded")
	}
}
