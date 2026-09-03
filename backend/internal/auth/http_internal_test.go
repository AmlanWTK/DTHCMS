package auth

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

// clientDigest is what the per-client throttle keys on, so what it ignores matters as
// much as what it hashes.

func TestClientDigestIsTheSameAcrossConnections(t *testing.T) {
	// The ephemeral port changes every time a client reconnects. If it were part of the
	// digest, reconnecting between attempts would reset the throttle — which is exactly
	// what a script does without trying.
	first := httptest.NewRequest("POST", "/v1/auth/login", nil)
	first.RemoteAddr = "203.0.113.7:51234"
	second := httptest.NewRequest("POST", "/v1/auth/login", nil)
	second.RemoteAddr = "203.0.113.7:51235"

	if !bytes.Equal(clientDigest(first), clientDigest(second)) {
		t.Fatal("the same host on two ports produced two digests; the per-client throttle " +
			"resets on every reconnect")
	}
}

func TestClientDigestDistinguishesHosts(t *testing.T) {
	a := httptest.NewRequest("POST", "/v1/auth/login", nil)
	a.RemoteAddr = "203.0.113.7:51234"
	b := httptest.NewRequest("POST", "/v1/auth/login", nil)
	b.RemoteAddr = "203.0.113.8:51234"

	if bytes.Equal(clientDigest(a), clientDigest(b)) {
		t.Fatal("two hosts produced one digest")
	}
}

func TestClientDigestIgnoresForwardedFor(t *testing.T) {
	// A header the client sets is a throttle the client can escape.
	plain := httptest.NewRequest("POST", "/v1/auth/login", nil)
	plain.RemoteAddr = "203.0.113.7:51234"

	spoofed := httptest.NewRequest("POST", "/v1/auth/login", nil)
	spoofed.RemoteAddr = "203.0.113.7:51234"
	spoofed.Header.Set("X-Forwarded-For", "198.51.100.1")
	spoofed.Header.Set("X-Real-IP", "198.51.100.2")

	if !bytes.Equal(clientDigest(plain), clientDigest(spoofed)) {
		t.Fatal("a forwarding header changed the digest; the socket address is the only " +
			"thing the client cannot choose")
	}
}

func TestClientDigestHandlesIPv6AndBareAddresses(t *testing.T) {
	v6 := httptest.NewRequest("POST", "/v1/auth/login", nil)
	v6.RemoteAddr = "[2001:db8::1]:443"
	v6again := httptest.NewRequest("POST", "/v1/auth/login", nil)
	v6again.RemoteAddr = "[2001:db8::1]:8443"
	if !bytes.Equal(clientDigest(v6), clientDigest(v6again)) {
		t.Fatal("an IPv6 host on two ports produced two digests")
	}

	bare := httptest.NewRequest("POST", "/v1/auth/login", nil)
	bare.RemoteAddr = "203.0.113.7"
	if clientDigest(bare) == nil {
		t.Fatal("a bare address with no port should still produce a digest")
	}

	none := httptest.NewRequest("POST", "/v1/auth/login", nil)
	none.RemoteAddr = ""
	if clientDigest(none) != nil {
		t.Fatal("an unknown address must produce no digest, so the per-code throttle alone applies")
	}
}
