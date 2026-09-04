package blobstore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/clock"
)

// The S3-compatible adapter (CP34).
//
// Written against the standard library rather than a vendored SDK, for one reason that is
// worth stating plainly: this deployment has no module proxy, so the choice was between an
// SDK that cannot be fetched and about two hundred lines of a very well-specified signing
// algorithm. SigV4 is arithmetic — a canonical request, a string to sign, four HMACs — and
// it is exercised here against a real server on every test run.
//
// It speaks to MinIO on a developer's machine and to Google Cloud Storage's interoperability
// endpoint in production, which is what makes "one class moves to Bangladesh without
// touching a call site" (D-01) a configuration change.
//
// Two properties this adapter is responsible for, both from the port's own doc comment:
//
//	nothing is ever public   Objects are written with no ACL at all. Every read is a
//	                         pre-signed GET with a short expiry, and the signature is over
//	                         the method as well as the path, so a URL minted to read cannot
//	                         be replayed to write.
//	classes are addresses    A caller names a data class; the bucket is looked up. There is
//	                         no way to pass a bucket name in, which is what stops an
//	                         identifier-class photograph being written to the documents
//	                         bucket by a typo.

// S3Config is what the adapter needs.
type S3Config struct {
	// Endpoint is the host, with a scheme: https://storage.googleapis.com, http://127.0.0.1:9000.
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	// Buckets maps a data class to the bucket that holds it.
	Buckets map[Class]string
	// PathStyle addresses buckets as /bucket/key rather than by subdomain. MinIO wants it;
	// a real cloud endpoint usually does not.
	PathStyle bool
	Clock     clock.Clock
	HTTP      *http.Client
}

// S3 is the adapter.
type S3 struct {
	cfg      S3Config
	endpoint *url.URL
}

var _ Store = (*S3)(nil)

// ErrUnknownClass is a data class with no bucket configured. Refused rather than defaulted:
// a photograph written to whichever bucket happened to be first is a photograph nobody can
// find and nobody has classified.
var ErrUnknownClass = errors.New("blobstore: no bucket configured for that data class")

// ErrNotFound is an object that is not there.
var ErrNotFound = errors.New("blobstore: no such object")

func NewS3(cfg S3Config) (*S3, error) {
	if cfg.Endpoint == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("blobstore: endpoint, access key and secret key are all required")
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("blobstore: endpoint: %w", err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, errors.New("blobstore: the endpoint needs a scheme and a host")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 60 * time.Second}
	}
	return &S3{cfg: cfg, endpoint: endpoint}, nil
}

func (s *S3) bucket(class Class) (string, error) {
	name, ok := s.cfg.Buckets[class]
	if !ok || name == "" {
		return "", fmt.Errorf("%w: %s", ErrUnknownClass, class)
	}
	return name, nil
}

func (s *S3) Put(ctx context.Context, class Class, key string, r io.Reader, size int64, contentType string) (Object, error) {
	target, err := s.url(class, key)
	if err != nil {
		return Object{}, err
	}
	body, err := io.ReadAll(io.LimitReader(r, size))
	if err != nil {
		return Object{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target.String(), strings.NewReader(string(body)))
	if err != nil {
		return Object{}, err
	}
	req.ContentLength = int64(len(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// No ACL header at all. The bucket's own policy is what keeps objects private, and a
	// call that could set one is a call that could set `public-read` by a typo.
	if err := s.sign(req, sha256hex(body)); err != nil {
		return Object{}, err
	}
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return Object{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return Object{}, s.fail("put", resp)
	}
	return Object{
		Class: class, Key: key, Size: int64(len(body)),
		ContentType: contentType, ModifiedAt: s.cfg.Clock.Now().UTC(),
	}, nil
}

func (s *S3) Get(ctx context.Context, class Class, key string) (io.ReadCloser, error) {
	target, err := s.url(class, key)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	if err := s.sign(req, emptyPayload); err != nil {
		return nil, err
	}
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, s.fail("get", resp)
	}
	return resp.Body, nil
}

func (s *S3) Stat(ctx context.Context, class Class, key string) (Object, error) {
	target, err := s.url(class, key)
	if err != nil {
		return Object{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target.String(), nil)
	if err != nil {
		return Object{}, err
	}
	if err := s.sign(req, emptyPayload); err != nil {
		return Object{}, err
	}
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return Object{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return Object{}, s.fail("stat", resp)
	}
	size, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	modified, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	return Object{
		Class: class, Key: key, Size: size,
		ContentType: resp.Header.Get("Content-Type"), ModifiedAt: modified,
	}, nil
}

func (s *S3) Delete(ctx context.Context, class Class, key string) error {
	target, err := s.url(class, key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target.String(), nil)
	if err != nil {
		return err
	}
	if err := s.sign(req, emptyPayload); err != nil {
		return err
	}
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return s.fail("delete", resp)
	}
	return nil
}

// Check reaches the endpoint. A failing store must fail readiness rather than the first
// photograph of the morning.
func (s *S3) Check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.endpoint.String()+"/", nil)
	if err != nil {
		return err
	}
	if err := s.sign(req, emptyPayload); err != nil {
		return err
	}
	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// Any answer at all means the endpoint is there. A 403 to a list-buckets call is a
	// correctly locked-down store, not an outage.
	return nil
}

// SignedURL is the only way a client ever reaches an object.
//
// A query-signed GET: the signature covers the method, the path, the expiry and the host,
// so a URL minted to read one photograph cannot be edited into a URL that reads another or
// that writes anything.
func (s *S3) SignedURL(ctx context.Context, class Class, key string, ttl time.Duration) (string, error) {
	return s.presign(ctx, http.MethodGet, class, key, ttl, "")
}

// SignedUpload is a pre-signed PUT, so a tablet uploads a photograph straight to storage
// rather than through the API.
//
// The reason is not only bandwidth. A photograph that never passes through the application
// is a photograph the application cannot log, cache, or accidentally write to a temporary
// file — and the request body of an API server is the classic place a PHI image ends up in
// a crash dump.
func (s *S3) SignedUpload(ctx context.Context, class Class, key string, ttl time.Duration, contentType string) (string, error) {
	return s.presign(ctx, http.MethodPut, class, key, ttl, contentType)
}

const (
	algorithm    = "AWS4-HMAC-SHA256"
	emptyPayload = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	unsigned     = "UNSIGNED-PAYLOAD"
	// MaxSignedTTL is the longest a signed URL may live.
	//
	// Fifteen minutes: long enough for a slow upload on a clinic's wifi, short enough that
	// a URL copied out of a browser's history or a proxy log is useless by the time anybody
	// finds it. The plan's "short TTL" is made a number here so that it can be tested.
	MaxSignedTTL = 15 * time.Minute
)

func (s *S3) presign(_ context.Context, method string, class Class, key string, ttl time.Duration, contentType string) (string, error) {
	if ttl <= 0 || ttl > MaxSignedTTL {
		ttl = MaxSignedTTL
	}
	target, err := s.url(class, key)
	if err != nil {
		return "", err
	}
	now := s.cfg.Clock.Now().UTC()
	scope := fmt.Sprintf("%s/%s/s3/aws4_request", now.Format("20060102"), s.cfg.Region)

	query := url.Values{}
	query.Set("X-Amz-Algorithm", algorithm)
	query.Set("X-Amz-Credential", s.cfg.AccessKey+"/"+scope)
	query.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	query.Set("X-Amz-Expires", strconv.Itoa(int(ttl.Seconds())))
	query.Set("X-Amz-SignedHeaders", "host")

	canonical := strings.Join([]string{
		method,
		target.EscapedPath(),
		encodeQuery(query),
		"host:" + target.Host + "\n",
		"host",
		unsigned,
	}, "\n")

	stringToSign := strings.Join([]string{
		algorithm,
		now.Format("20060102T150405Z"),
		scope,
		sha256hex([]byte(canonical)),
	}, "\n")

	query.Set("X-Amz-Signature", hex.EncodeToString(hmacSHA256(s.signingKey(now), []byte(stringToSign))))
	target.RawQuery = encodeQuery(query)
	// Deliberately not signed into the URL: a Content-Type that is part of the signature
	// makes the client's exact header a condition of the upload succeeding, and a phone's
	// HTTP stack is not reliable about it. The kind of file is enforced where it can be —
	// on the key and on the stored object — rather than pretended at here.
	_ = contentType
	return target.String(), nil
}

func (s *S3) url(class Class, key string) (*url.URL, error) {
	bucket, err := s.bucket(class)
	if err != nil {
		return nil, err
	}
	if strings.Contains(key, "..") || strings.HasPrefix(key, "/") {
		// A key that can climb out of its prefix is a key that can read another class's
		// objects through a correctly signed URL.
		return nil, fmt.Errorf("blobstore: %q is not a safe object key", key)
	}
	out := *s.endpoint
	if s.cfg.PathStyle {
		out.Path = "/" + bucket + "/" + key
	} else {
		out.Host = bucket + "." + out.Host
		out.Path = "/" + key
	}
	return &out, nil
}

func (s *S3) sign(req *http.Request, payloadHash string) error {
	now := s.cfg.Clock.Now().UTC()
	stamp := now.Format("20060102T150405Z")
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if req.Host == "" {
		req.Host = req.URL.Host
	}

	headers := map[string]string{
		"host":                 req.URL.Host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           stamp,
	}
	if ct := req.Header.Get("Content-Type"); ct != "" {
		headers["content-type"] = ct
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + strings.TrimSpace(headers[name]) + "\n")
	}
	signedHeaders := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		encodeQuery(req.URL.Query()),
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := fmt.Sprintf("%s/%s/s3/aws4_request", now.Format("20060102"), s.cfg.Region)
	stringToSign := strings.Join([]string{
		algorithm, stamp, scope, sha256hex([]byte(canonical)),
	}, "\n")

	signature := hex.EncodeToString(hmacSHA256(s.signingKey(now), []byte(stringToSign)))
	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, s.cfg.AccessKey, scope, signedHeaders, signature))
	return nil
}

// signingKey is SigV4's four chained HMACs: the secret is never used directly, and the key
// is scoped to a day, a region and a service, so a leaked signing key expires by itself.
func (s *S3) signingKey(now time.Time) []byte {
	date := hmacSHA256([]byte("AWS4"+s.cfg.SecretKey), []byte(now.Format("20060102")))
	region := hmacSHA256(date, []byte(s.cfg.Region))
	service := hmacSHA256(region, []byte("s3"))
	return hmacSHA256(service, []byte("aws4_request"))
}

func (s *S3) fail(op string, resp *http.Response) error {
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	// The body is included because an S3 error body says which condition failed, and
	// without it "403" is unactionable. It carries no object content.
	return fmt.Errorf("blobstore: %s failed with %d: %s", op, resp.StatusCode, strings.TrimSpace(string(body)))
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// encodeQuery is url.Values.Encode with S3's escaping: a space is %20, not +, and the
// parameters are sorted. Getting this wrong produces a signature mismatch and no clue why.
func encodeQuery(values url.Values) string {
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
			out.WriteString(escape(key) + "=" + escape(value))
		}
	}
	return out.String()
}

func escape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
