// Package blobtest runs a small S3-compatible object store in the test process.
//
// It exists because a fake that accepts anything would prove nothing about the hand-written
// SigV4 in the adapter beside it: the whole risk there is producing a signature the real
// thing rejects, and the only way to catch that is to recompute the signature server-side
// and compare. So this server does exactly what MinIO does — verifies the signature, honours
// the expiry on a pre-signed URL, and refuses everything else.
//
// It is also what lets the patient module's photograph tests (CP34) exercise the real upload
// path end to end rather than mocking the store away, which would leave the one thing worth
// testing — that the server reads the object back and does not take the client's word for
// what was uploaded — untested.
package blobtest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/blobstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
)

// The credentials and region the in-process store expects. Fixed, because a test that
// generates them has to thread them through everything and gains nothing.
const (
	AccessKey = "dthcms-local"
	SecretKey = "dthcms-local-secret"
	Region    = "ap-south-1"
)

// fakeS3 is a small object store that checks SigV4 the way MinIO does.
type Store struct {
	base    string
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
	// refused counts requests whose signature did not verify, so a test can assert that a
	// tampered URL was rejected rather than merely 404ing for some other reason.
	refused int
}

func newServer(t *testing.T) (*Store, *httptest.Server) {
	t.Helper()
	store := &Store{objects: map[string][]byte{}, types: map[string]string{}}
	server := httptest.NewServer(store)
	store.base = server.URL
	t.Cleanup(server.Close)
	return store, server
}

func (f *Store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Expiry and signature are distinguished, because "403" for both is the single most
	// time-wasting failure to debug: a correct signature on a stale URL looks identical to
	// a broken signing implementation.
	if code := f.verify(r); code != "" {
		f.mu.Lock()
		f.refused++
		f.mu.Unlock()
		http.Error(w, "<Error><Code>"+code+"</Code></Error>", http.StatusForbidden)
		return
	}

	key := r.URL.Path
	f.mu.Lock()
	defer f.mu.Unlock()

	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		f.objects[key] = body
		f.types[key] = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		body, ok := f.objects[key]
		if !ok {
			http.Error(w, "<Error><Code>NoSuchKey</Code></Error>", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", f.types[key])
		_, _ = w.Write(body)
	case http.MethodHead:
		body, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.Header().Set("Content-Type", f.types[key])
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// verify recomputes the signature. Both forms: a header-signed request and a query-signed
// (pre-signed) URL.
// verify returns "" when the request is good, or the S3 error code that explains it.
func (f *Store) verify(r *http.Request) string {
	if r.URL.Query().Get("X-Amz-Signature") != "" {
		return f.verifyPresigned(r)
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		return "AccessDenied"
	}
	parts := map[string]string{}
	for _, chunk := range strings.Split(strings.TrimPrefix(auth, "AWS4-HMAC-SHA256 "), ", ") {
		key, value, _ := strings.Cut(chunk, "=")
		parts[key] = value
	}
	stamp := r.Header.Get("X-Amz-Date")
	payload := r.Header.Get("X-Amz-Content-Sha256")

	var canonicalHeaders strings.Builder
	names := strings.Split(parts["SignedHeaders"], ";")
	for _, name := range names {
		switch name {
		case "host":
			canonicalHeaders.WriteString("host:" + r.Host + "\n")
		default:
			canonicalHeaders.WriteString(name + ":" + strings.TrimSpace(r.Header.Get(name)) + "\n")
		}
	}
	canonical := strings.Join([]string{
		r.Method, r.URL.EscapedPath(), canonicalQuery(r.URL.Query()),
		canonicalHeaders.String(), parts["SignedHeaders"], payload,
	}, "\n")
	if parts["Signature"] != sign(stamp, canonical) {
		return "SignatureDoesNotMatch"
	}
	return ""
}

func (f *Store) verifyPresigned(r *http.Request) string {
	query := r.URL.Query()
	claimed := query.Get("X-Amz-Signature")
	stamp := query.Get("X-Amz-Date")

	// Expiry is enforced, as a real store enforces it.
	signedAt, err := time.Parse("20060102T150405Z", stamp)
	if err != nil {
		return "AuthorizationQueryParametersError"
	}
	ttl, _ := time.ParseDuration(query.Get("X-Amz-Expires") + "s")
	if time.Since(signedAt) > ttl {
		return "AccessDenied: Request has expired"
	}

	query.Del("X-Amz-Signature")
	canonical := strings.Join([]string{
		r.Method, r.URL.EscapedPath(), canonicalQuery(query),
		"host:" + r.Host + "\n", "host", "UNSIGNED-PAYLOAD",
	}, "\n")
	if claimed != sign(stamp, canonical) {
		return "SignatureDoesNotMatch"
	}
	return ""
}

func sign(stamp, canonical string) string {
	day := stamp[:8]
	scope := fmt.Sprintf("%s/%s/s3/aws4_request", day, Region)
	sum := sha256.Sum256([]byte(canonical))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", stamp, scope, hex.EncodeToString(sum[:]),
	}, "\n")

	mac := func(key, data []byte) []byte {
		h := hmac.New(sha256.New, key)
		h.Write(data)
		return h.Sum(nil)
	}
	k := mac([]byte("AWS4"+SecretKey), []byte(day))
	k = mac(k, []byte(Region))
	k = mac(k, []byte("s3"))
	k = mac(k, []byte("aws4_request"))
	return hex.EncodeToString(mac(k, []byte(stringToSign)))
}

func canonicalQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, key := range keys {
		for _, value := range values[key] {
			if out.Len() > 0 {
				out.WriteByte('&')
			}
			out.WriteString(strings.ReplaceAll(url.QueryEscape(key), "+", "%20"))
			out.WriteByte('=')
			out.WriteString(strings.ReplaceAll(url.QueryEscape(value), "+", "%20"))
		}
	}
	return out.String()
}

// Refused is how many requests failed signature verification. A test asserting that a
// tampered URL was rejected needs to know it failed *for that reason* rather than by 404.
func (f *Store) Refused() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refused
}

// New starts the store and returns an adapter pointed at it, with the identifier and
// document classes mapped to buckets.
//
// `now` fixes the adapter's clock, which is what lets a test mint a URL that is already
// expired without waiting for it.
func New(t *testing.T, now time.Time) (*Store, *blobstore.S3) {
	t.Helper()
	fake, server := newServer(t)
	adapter, err := blobstore.NewS3(blobstore.S3Config{
		Endpoint: server.URL, Region: Region,
		AccessKey: AccessKey, SecretKey: SecretKey,
		Buckets: map[blobstore.Class]string{
			blobstore.ClassIdentifier: "dthcms-identifier",
			blobstore.ClassDocument:   "dthcms-document",
		},
		PathStyle: true,
		Clock:     clock.NewFixed(now),
	})
	if err != nil {
		t.Fatal(err)
	}
	return fake, adapter
}

// URL is the store's base address, for a test that wants to make an unsigned request.
func (f *Store) URL() string { return f.base }
