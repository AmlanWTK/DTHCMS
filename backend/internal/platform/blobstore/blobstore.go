// Package blobstore defines how DTHCMS stores files: patient photographs, scanned
// medical records, rendered prescriptions.
//
// Only the port lives here. The adapter arrives with the first checkpoint that actually
// stores something (CP34, patient photographs), and the production Google Cloud Storage
// adapter with the cloud project (CP03). Defining the port now keeps two properties out
// of every caller's hands:
//
//   - Objects are addressed by data class, not by raw bucket name, so if the Personal
//     Data Protection Act requires identifier-class data to stay in Bangladesh (D-01),
//     one class moves without touching a single call site.
//   - Access is always through a short-lived signed URL. No object is ever public, in
//     any environment.
package blobstore

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Class is the data classification of an object, which determines where it may be
// stored and how long it is kept (implementation plan section 9.6).
type Class string

const (
	// ClassIdentifier: photographs, national ID images, biometric artefacts.
	// The most regulated category under D-01.
	ClassIdentifier Class = "identifier"
	// ClassDocument: scanned external medical records.
	ClassDocument Class = "document"
	// ClassDerived: generated artefacts such as rendered prescription PDFs.
	ClassDerived Class = "derived"
)

// Valid reports whether c is a known data class.
func (c Class) Valid() bool {
	switch c {
	case ClassIdentifier, ClassDocument, ClassDerived:
		return true
	default:
		return false
	}
}

// Object describes a stored object.
type Object struct {
	Class       Class
	Key         string
	Size        int64
	ContentType string
	ModifiedAt  time.Time
}

// Store is the port every caller uses.
type Store interface {
	// Put stores an object and returns its key.
	Put(ctx context.Context, class Class, key string, r io.Reader, size int64, contentType string) (Object, error)
	// Get opens an object for reading. The caller closes it.
	Get(ctx context.Context, class Class, key string) (io.ReadCloser, error)
	// Stat returns metadata without transferring the object.
	Stat(ctx context.Context, class Class, key string) (Object, error)
	// SignedURL returns a time-limited URL. This is the only way a client ever reaches
	// an object: nothing is public, ever.
	SignedURL(ctx context.Context, class Class, key string, ttl time.Duration) (string, error)
	// Delete removes an object. Clinical documents are not deleted through this path —
	// retention and erasure are governed by policy (D-05).
	Delete(ctx context.Context, class Class, key string) error
	// Check reports whether the store is reachable.
	Check(ctx context.Context) error
}

// ErrNotConfigured is returned by the placeholder store. The interface exists from CP05
// so that call sites can be written against it; the adapter lands at CP34.
var ErrNotConfigured = fmt.Errorf("blobstore: no adapter configured (arrives at CP34)")

// Unconfigured is a Store that fails every operation with a clear explanation. It is
// wired in until a real adapter exists, so that an accidental early use fails loudly
// rather than appearing to succeed.
type Unconfigured struct{}

func (Unconfigured) Put(context.Context, Class, string, io.Reader, int64, string) (Object, error) {
	return Object{}, ErrNotConfigured
}

func (Unconfigured) Get(context.Context, Class, string) (io.ReadCloser, error) {
	return nil, ErrNotConfigured
}

func (Unconfigured) Stat(context.Context, Class, string) (Object, error) {
	return Object{}, ErrNotConfigured
}

func (Unconfigured) SignedURL(context.Context, Class, string, time.Duration) (string, error) {
	return "", ErrNotConfigured
}

func (Unconfigured) Delete(context.Context, Class, string) error { return ErrNotConfigured }

// Check reports healthy. The readiness endpoint must not fail because a component that
// does not exist yet is absent.
func (Unconfigured) Check(context.Context) error { return nil }
