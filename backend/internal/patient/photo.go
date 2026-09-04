package patient

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AmlanWTK/DTHCMS/backend/internal/eventstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/blobstore"
	"github.com/AmlanWTK/DTHCMS/backend/internal/platform/dbgen"
)

// Patient photographs (CP34).
//
// The upload does not pass through the API. A tablet asks for a pre-signed PUT, uploads the
// bytes straight to storage, and then tells the API the object is there — at which point the
// server reads it back, checks its size and digest, writes the row and appends the event.
//
// The bandwidth saving is the least of it. A photograph that never enters the API process is
// a photograph that cannot end up in a request log, a crash dump, a reverse proxy's buffer
// or a temporary file — all of which are places PHI images are found in incident reports and
// none of which anyone intended.
//
// The **key is the server's**, always. A key a client could choose is a key that can be
// pointed at somebody else's photograph, and a correctly signed URL would then serve it.

var (
	// ErrNoPhoto is a patient with no photograph on file.
	ErrNoPhoto = errors.New("patient: no photograph on file")
	// ErrPhotoNotUploaded means the client claimed an upload that is not in storage. The
	// commonest cause is an upload that failed and was not retried, and the right answer is
	// to say so rather than to write a row pointing at nothing.
	ErrPhotoNotUploaded = errors.New("patient: no object was uploaded to that key")
	// ErrPhotoMismatch means the object in storage is not the one the client described.
	ErrPhotoMismatch = errors.New("patient: the uploaded object does not match what was declared")
)

// PhotoService issues upload URLs and attaches what was uploaded.
type PhotoService struct {
	store  *Store
	events *eventstore.Store
	blobs  blobstore.Store
	clock  interface{ Now() time.Time }
}

func NewPhotoService(store *Store, events *eventstore.Store, blobs blobstore.Store, clk interface{ Now() time.Time }) *PhotoService {
	return &PhotoService{store: store, events: events, blobs: blobs, clock: clk}
}

// UploadTicket is what a client needs to put a photograph in storage.
type UploadTicket struct {
	// ObjectKey is the server's, and must come back unchanged when the client attaches.
	ObjectKey string    `json:"object_key"`
	URL       string    `json:"upload_url"`
	ExpiresAt time.Time `json:"expires_at"`
	// MaxBytes is stated so a client can refuse a file before spending a clinic's uplink
	// on it. The server refuses it again on attach.
	MaxBytes int64 `json:"max_bytes"`
	// ContentTypes are the ones that will be accepted.
	ContentTypes []string `json:"content_types"`
}

// PhotoTTL is how long an upload URL lives. Long enough for a slow upload on clinic wifi,
// short enough that a URL out of a browser's history is useless.
const PhotoTTL = 10 * time.Minute

// IssueUpload mints a pre-signed PUT for a patient's next photograph.
func (s *PhotoService) IssueUpload(ctx context.Context, patientID uuid.UUID, contentType string) (UploadTicket, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return UploadTicket{}, err
	}
	// The patient must exist and be this facility's before a URL is minted: an upload
	// ticket for an id the caller may not see is a way to learn that the id exists.
	if _, err := s.store.ByID(ctx, patientID, actor.FacilityID()); err != nil {
		return UploadTicket{}, err
	}
	if !allowedPhotoType(contentType) {
		return UploadTicket{}, fmt.Errorf("%w: %s is not an image this clinic stores", ErrPhotoMismatch, contentType)
	}

	key, err := photoKey(patientID, contentType)
	if err != nil {
		return UploadTicket{}, err
	}
	url, err := s.upload(ctx, key, contentType)
	if err != nil {
		return UploadTicket{}, err
	}
	return UploadTicket{
		ObjectKey: key, URL: url,
		ExpiresAt:    s.clock.Now().UTC().Add(PhotoTTL),
		MaxBytes:     eventstore.MaxPhotoBytes,
		ContentTypes: photoTypes(),
	}, nil
}

// Attach records a photograph the client has already uploaded.
//
// The server reads the object back before believing anything about it. A client that says
// "two megabytes of JPEG" and uploaded nothing, or uploaded eight megabytes of something
// else, is a client whose word cannot be the record — and the row would then point at an
// object nobody can render.
func (s *PhotoService) Attach(ctx context.Context, patientID uuid.UUID, in AttachPhoto) (dbgen.CorePatientPhoto, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return dbgen.CorePatientPhoto{}, err
	}
	if _, err := s.store.ByID(ctx, patientID, actor.FacilityID()); err != nil {
		return dbgen.CorePatientPhoto{}, err
	}
	if !strings.HasPrefix(in.ObjectKey, photoPrefix(patientID)) {
		// The key is the server's. One that does not belong to this patient is either a
		// client bug or an attempt to attach somebody else's photograph to this record.
		return dbgen.CorePatientPhoto{}, fmt.Errorf("%w: that key is not this patient's", ErrPhotoMismatch)
	}

	size, digest, err := s.inspect(ctx, in.ObjectKey)
	if err != nil {
		return dbgen.CorePatientPhoto{}, err
	}
	if size > eventstore.MaxPhotoBytes {
		return dbgen.CorePatientPhoto{}, fmt.Errorf("%w: %d bytes is over the limit", ErrPhotoMismatch, size)
	}

	previous, hasPrevious, err := s.current(ctx, patientID, actor.FacilityID())
	if err != nil {
		return dbgen.CorePatientPhoto{}, err
	}

	payload, err := json.Marshal(eventstore.PatientPhotoCaptured{
		FacilityID: actor.FacilityID().String(), PatientID: patientID.String(),
		ObjectClass: string(blobstore.ClassIdentifier), ObjectKey: in.ObjectKey,
		ContentType: in.ContentType, ByteSize: size, SHA256: digest,
		Width: in.Width, Height: in.Height,
		ReplacesKey: previousKey(previous, hasPrevious),
	})
	if err != nil {
		return dbgen.CorePatientPhoto{}, err
	}

	raw, err := hex.DecodeString(digest)
	if err != nil {
		return dbgen.CorePatientPhoto{}, err
	}
	now := s.clock.Now().UTC()

	var written dbgen.CorePatientPhoto
	err = s.store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx, q *dbgen.Queries) error {
		id := patientID
		if _, err := s.events.AppendInTx(ctx, tx, eventstore.Envelope{
			EventID: in.EventID, AggregateType: "PATIENT", AggregateID: patientID, PatientID: &id,
			EventType: "PATIENT_PHOTO_CAPTURED", EventVersion: 1,
			OccurredAt: now, Actor: actor, Source: in.Source, Payload: payload,
		}); err != nil {
			return err
		}
		// The previous photograph is retired, not deleted: a chart printed last month
		// showing a different face has to be explicable.
		if hasPrevious {
			if err := q.RetirePatientPhoto(ctx, dbgen.RetirePatientPhotoParams{
				ID: previous.ID, ReplacedAt: &now,
			}); err != nil {
				return err
			}
		}
		device := actor.DeviceID()
		row, err := q.InsertPatientPhoto(ctx, dbgen.InsertPatientPhotoParams{
			FacilityID: actor.FacilityID(), PatientID: patientID,
			ObjectClass: string(blobstore.ClassIdentifier), ObjectKey: in.ObjectKey,
			ContentType: in.ContentType, ByteSize: int32(size), //nolint:gosec // capped above
			Sha256: raw, Width: optionalInt(in.Width), Height: optionalInt(in.Height),
			CapturedBy: actor.UserID(), CapturedAt: now,
			DeviceID: uuid.NullUUID{UUID: device, Valid: device != uuid.Nil},
			EventID:  in.EventID, ReplacesID: replacesID(previous, hasPrevious),
		})
		if err != nil {
			return err
		}
		written = row
		return nil
	})
	if err != nil {
		return dbgen.CorePatientPhoto{}, err
	}
	return written, nil
}

// AttachPhoto is what a client says about what it uploaded. Everything in it that matters is
// verified against the object itself.
type AttachPhoto struct {
	EventID     uuid.UUID
	ObjectKey   string
	ContentType string
	Width       int
	Height      int
	Source      eventstore.Source
}

// ViewURL is a short-lived signed URL for the current photograph.
//
// Minted per request, never stored. A URL in a database row is a URL that has expired by the
// time anybody reads it and cannot be told from one that never worked.
func (s *PhotoService) ViewURL(ctx context.Context, patientID uuid.UUID, ttl time.Duration) (string, time.Time, error) {
	actor, err := eventstore.ActorFrom(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	photo, ok, err := s.current(ctx, patientID, actor.FacilityID())
	if err != nil {
		return "", time.Time{}, err
	}
	if !ok {
		return "", time.Time{}, ErrNoPhoto
	}
	if ttl <= 0 || ttl > blobstore.MaxSignedTTL {
		ttl = blobstore.MaxSignedTTL
	}
	url, err := s.blobs.SignedURL(ctx, blobstore.Class(photo.ObjectClass), photo.ObjectKey, ttl)
	if err != nil {
		return "", time.Time{}, err
	}
	return url, s.clock.Now().UTC().Add(ttl), nil
}

// --- helpers ---

func (s *PhotoService) current(ctx context.Context, patientID, facility uuid.UUID) (dbgen.CorePatientPhoto, bool, error) {
	row, err := s.store.q.CurrentPatientPhoto(ctx, dbgen.CurrentPatientPhotoParams{
		PatientID: patientID, FacilityID: facility,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dbgen.CorePatientPhoto{}, false, nil
		}
		return dbgen.CorePatientPhoto{}, false, err
	}
	return row, true, nil
}

// inspect reads the object back and returns what it actually is.
func (s *PhotoService) inspect(ctx context.Context, key string) (int64, string, error) {
	reader, err := s.blobs.Get(ctx, blobstore.ClassIdentifier, key)
	if err != nil {
		if errors.Is(err, blobstore.ErrNotFound) {
			return 0, "", ErrPhotoNotUploaded
		}
		return 0, "", err
	}
	defer func() { _ = reader.Close() }()

	// Read at most one byte more than the limit: enough to know it is over without pulling
	// an arbitrarily large object into the API's memory, which is the thing the direct
	// upload exists to avoid.
	digest, size, err := hashUpTo(reader, eventstore.MaxPhotoBytes+1)
	if err != nil {
		return 0, "", err
	}
	if size == 0 {
		return 0, "", ErrPhotoNotUploaded
	}
	return size, digest, nil
}

func (s *PhotoService) upload(ctx context.Context, key, contentType string) (string, error) {
	signer, ok := s.blobs.(interface {
		SignedUpload(context.Context, blobstore.Class, string, time.Duration, string) (string, error)
	})
	if !ok {
		return "", errors.New("patient: the configured object store cannot issue upload URLs")
	}
	return signer.SignedUpload(ctx, blobstore.ClassIdentifier, key, PhotoTTL, contentType)
}

func hashUpTo(r io.Reader, limit int64) (string, int64, error) {
	hasher := sha256.New()
	size, err := io.Copy(hasher, io.LimitReader(r, limit))
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

// photoKey is `patients/<patient>/<random>.<ext>`.
//
// Random rather than sequential, so a key does not say how many photographs a patient has —
// and, more to the point, so that an old signed URL cannot be edited into the next one.
func photoKey(patientID uuid.UUID, contentType string) (string, error) {
	suffix := make([]byte, 16)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s%s", photoPrefix(patientID), hex.EncodeToString(suffix), extensionFor(contentType)), nil
}

func photoPrefix(patientID uuid.UUID) string {
	return "patients/" + patientID.String() + "/photo-"
}

func extensionFor(contentType string) string {
	switch contentType {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

func photoTypes() []string { return []string{"image/jpeg", "image/png", "image/webp"} }

func allowedPhotoType(contentType string) bool {
	for _, allowed := range photoTypes() {
		if allowed == contentType {
			return true
		}
	}
	return false
}

func previousKey(previous dbgen.CorePatientPhoto, ok bool) string {
	if !ok {
		return ""
	}
	return previous.ObjectKey
}

func replacesID(previous dbgen.CorePatientPhoto, ok bool) uuid.NullUUID {
	if !ok {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: previous.ID, Valid: true}
}

func optionalInt(n int) *int32 {
	if n <= 0 {
		return nil
	}
	//nolint:gosec // a pixel dimension
	small := int32(n)
	return &small
}
