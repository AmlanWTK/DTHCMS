package blobstore_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/blobstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/blobstore/blobtest"
)

// The S3 adapter against a server that actually verifies the signature (CP34).
//
// A fake that accepts anything would prove nothing: the whole risk in hand-written SigV4 is
// that it produces a signature the real thing rejects, and the only way to catch that is to
// recompute the signature server-side and compare. That is what `fakeS3` does, and it is
// why these tests are worth their length.

// The in-process store lives in blobtest, so the patient module's photograph tests can
// exercise the same real upload path (CP34).

var fixed = time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)

// --- the signature ---

func TestAStoredObjectComesBack(t *testing.T) {
	// The round trip, against a server that verifies the signature. If the hand-written
	// SigV4 were wrong this would 403 rather than fail on the bytes.
	fake, store := blobtest.New(t, fixed)
	ctx := context.Background()

	photo := []byte("\xff\xd8\xff\xe0 not really a jpeg")
	if _, err := store.Put(ctx, blobstore.ClassIdentifier, "patients/p-1/photo.jpg",
		strings.NewReader(string(photo)), int64(len(photo)), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if fake.Refused() != 0 {
		t.Fatalf("the store refused %d signed requests", fake.Refused())
	}

	reader, err := store.Get(ctx, blobstore.ClassIdentifier, "patients/p-1/photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	got, _ := io.ReadAll(reader)
	if string(got) != string(photo) {
		t.Errorf("got %q", got)
	}

	meta, err := store.Stat(ctx, blobstore.ClassIdentifier, "patients/p-1/photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Size != int64(len(photo)) || meta.ContentType != "image/jpeg" {
		t.Errorf("stat = %+v", meta)
	}
}

func TestAMissingObjectIsNotFoundRatherThanAnError(t *testing.T) {
	_, store := blobtest.New(t, fixed)
	if _, err := store.Get(context.Background(), blobstore.ClassIdentifier, "nobody"); err == nil {
		t.Fatal("a missing object was returned")
	} else if !strings.Contains(err.Error(), "no such object") {
		t.Errorf("err = %v", err)
	}
}

// --- signed URLs ---

func TestASignedURLReadsAndNothingElseDoes(t *testing.T) {
	// "No object is ever public, in any environment" is the port's promise. This is what
	// makes it true rather than stated.
	fake, store := blobtest.New(t, time.Now().UTC())
	ctx := context.Background()

	if _, err := store.Put(ctx, blobstore.ClassIdentifier, "patients/p-1/photo.jpg",
		strings.NewReader("a photograph"), 12, "image/jpeg"); err != nil {
		t.Fatal(err)
	}

	// Unsigned: refused.
	plain, err := http.Get(fake.URL() + "/dthcms-identifier/patients/p-1/photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	_ = plain.Body.Close()
	if plain.StatusCode != http.StatusForbidden {
		t.Errorf("an unsigned request returned %d; the object is public", plain.StatusCode)
	}

	// Signed: works.
	signed, err := store.SignedURL(ctx, blobstore.ClassIdentifier, "patients/p-1/photo.jpg", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(signed) //nolint:gosec,noctx // the URL under test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the signed URL returned %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "a photograph" {
		t.Errorf("got %q", body)
	}
	if fake.Refused() != 1 {
		t.Errorf("the store refused %d requests; exactly the unsigned one should fail", fake.Refused())
	}
}

func TestASignedURLExpires(t *testing.T) {
	// Acceptance criterion 2. The whole value of a short TTL is that a URL in a browser's
	// history, a proxy log or a screenshot is useless by the time anybody finds it.
	// Signed as of twenty minutes ago, for a one-minute life.
	_, store := blobtest.New(t, time.Now().UTC().Add(-20*time.Minute))
	live := store
	ctx := context.Background()

	if _, err := live.Put(ctx, blobstore.ClassIdentifier, "p.jpg", strings.NewReader("x"), 1, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	stale, err := store.SignedURL(ctx, blobstore.ClassIdentifier, "p.jpg", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(stale) //nolint:gosec,noctx // the URL under test
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("an expired URL returned %d", resp.StatusCode)
	}
}

func TestASignedURLCannotBeEditedIntoAnother(t *testing.T) {
	// The signature covers the path and the method, so a URL minted to read one
	// photograph is not a URL that reads another or that writes anything.
	_, store := blobtest.New(t, time.Now().UTC())
	ctx := context.Background()

	for _, key := range []string{"mine.jpg", "somebody-elses.jpg"} {
		if _, err := store.Put(ctx, blobstore.ClassIdentifier, key, strings.NewReader("x"), 1, "image/jpeg"); err != nil {
			t.Fatal(err)
		}
	}
	signed, err := store.SignedURL(ctx, blobstore.ClassIdentifier, "mine.jpg", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	tampered := strings.Replace(signed, "mine.jpg", "somebody-elses.jpg", 1)
	resp, err := http.Get(tampered) //nolint:gosec,noctx // the URL under test
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("an edited URL read another object: %d", resp.StatusCode)
	}

	// And a read URL is not a write URL.
	write, err := http.NewRequest(http.MethodPut, signed, strings.NewReader("overwritten")) //nolint:noctx // the URL under test
	if err != nil {
		t.Fatal(err)
	}
	written, err := http.DefaultClient.Do(write)
	if err != nil {
		t.Fatal(err)
	}
	_ = written.Body.Close()
	if written.StatusCode != http.StatusForbidden {
		t.Errorf("a read URL was used to write: %d", written.StatusCode)
	}
}

func TestASignedUploadIsAPutAndNothingLonger(t *testing.T) {
	_, store := blobtest.New(t, time.Now().UTC())
	ctx := context.Background()

	upload, err := store.SignedUpload(ctx, blobstore.ClassIdentifier, "patients/p-1/photo.jpg", time.Minute, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, upload, strings.NewReader("a photograph")) //nolint:noctx // the URL under test
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "image/jpeg")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the upload URL returned %d", resp.StatusCode)
	}

	reader, err := store.Get(ctx, blobstore.ClassIdentifier, "patients/p-1/photo.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	body, _ := io.ReadAll(reader)
	if string(body) != "a photograph" {
		t.Errorf("got %q", body)
	}
}

func TestATTLIsCappedRatherThanTrusted(t *testing.T) {
	// A caller asking for a day-long URL gets fifteen minutes. Capped rather than refused,
	// because the caller is this codebase and a refusal here is an outage; the number is
	// the policy and the policy is not the caller's to set.
	_, store := blobtest.New(t, fixed)
	signed, err := store.SignedURL(context.Background(), blobstore.ClassIdentifier, "p.jpg", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("X-Amz-Expires") != "900" {
		t.Errorf("X-Amz-Expires = %q, want 900", parsed.Query().Get("X-Amz-Expires"))
	}
}

// --- classes and keys ---

func TestAClassWithNoBucketIsRefused(t *testing.T) {
	// A photograph written to whichever bucket happened to be first is a photograph nobody
	// can find and nobody has classified.
	_, store := blobtest.New(t, fixed)
	_, err := store.Put(context.Background(), blobstore.ClassDerived, "x", strings.NewReader("x"), 1, "")
	if err == nil || !strings.Contains(err.Error(), "no bucket configured") {
		t.Errorf("err = %v", err)
	}
}

func TestAKeyCannotClimbOutOfItsPrefix(t *testing.T) {
	// A key that can climb is a key that reads another class's objects through a correctly
	// signed URL.
	_, store := blobtest.New(t, fixed)
	for _, key := range []string{"../dthcms-document/records.pdf", "/etc/passwd", "a/../../b"} {
		if _, err := store.SignedURL(context.Background(), blobstore.ClassIdentifier, key, time.Minute); err == nil {
			t.Errorf("%q was accepted as an object key", key)
		}
	}
}

func TestTheStoreRefusesToBeBuiltWithoutCredentials(t *testing.T) {
	for name, cfg := range map[string]blobstore.S3Config{
		"no endpoint": {AccessKey: "a", SecretKey: "b"},
		"no key":      {Endpoint: "http://x", SecretKey: "b"},
		"no secret":   {Endpoint: "http://x", AccessKey: "a"},
		"no scheme":   {Endpoint: "storage.example", AccessKey: "a", SecretKey: "b"},
	} {
		if _, err := blobstore.NewS3(cfg); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
