package patient_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Patient photographs, end to end (CP34).
//
// The upload path is the design under test: the API issues a URL, the client uploads
// directly to storage, the API is told and reads the object back. Every one of these tests
// goes through a real object store — the in-process S3 the blobstore package tests against —
// so "the server verifies what was uploaded" is exercised rather than asserted.

// jpeg is a small byte string standing in for a photograph. Its content does not matter;
// what matters is that the same bytes go in and come out and that the digest is computed
// from them rather than taken from the request.
var jpeg = append([]byte{0xff, 0xd8, 0xff, 0xe0}, []byte(strings.Repeat("photo", 200))...)

func (h *api) uploadTicket(t *testing.T, patientID string) map[string]any {
	t.Helper()
	resp, body := h.call(t, http.MethodPost, "/v1/patients/"+patientID+"/photo/upload-url",
		map[string]any{"content_type": "image/jpeg"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload-url returned %d: %v", resp.StatusCode, body)
	}
	return body
}

func putBytes(t *testing.T, url string, data []byte, contentType string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(data)) //nolint:noctx // the URL under test
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("the upload returned %d: %s", resp.StatusCode, body)
	}
}

func (h *api) attachPhoto(t *testing.T, patientID, key string) (*http.Response, map[string]any) {
	t.Helper()
	return h.call(t, http.MethodPost, "/v1/patients/"+patientID+"/photo", map[string]any{
		"event_id":     uuid.Must(uuid.NewV7()).String(),
		"object_key":   key,
		"content_type": "image/jpeg",
		"width":        640,
		"height":       640,
	})
}

func TestAPhotographGoesToStorageAndComesBackSigned(t *testing.T) {
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)

	ticket := h.uploadTicket(t, id)
	key := ticket["object_key"].(string)
	// The key is the server's, and it names the patient — so a key from another patient's
	// ticket cannot be attached here.
	if !strings.HasPrefix(key, "patients/"+id+"/photo-") {
		t.Fatalf("object_key = %q", key)
	}
	putBytes(t, ticket["upload_url"].(string), jpeg, "image/jpeg")

	resp, body := h.attachPhoto(t, id, key)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("attach returned %d: %v", resp.StatusCode, body)
	}
	photo := body["photo"].(map[string]any)
	// The size comes from the object, not from the request — the request never said one.
	if int(photo["byte_size"].(float64)) != len(jpeg) {
		t.Errorf("byte_size = %v, want %d", photo["byte_size"], len(jpeg))
	}

	// The URL in the response works, and returns exactly what was uploaded.
	got := fetch(t, photo["url"].(string))
	if !bytes.Equal(got, jpeg) {
		t.Errorf("the photograph came back as %d bytes", len(got))
	}

	// The digest in the row is of the bytes in storage.
	var stored []byte
	if err := h.SQL.QueryRow(
		`SELECT sha256 FROM core.patient_photo WHERE object_key = $1`, key).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(jpeg)
	if hex.EncodeToString(stored) != hex.EncodeToString(want[:]) {
		t.Error("the stored digest is not of the stored bytes")
	}

	// And one event, on the patient's aggregate.
	var events int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ledger.event WHERE event_type = 'PATIENT_PHOTO_CAPTURED'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Errorf("the ledger holds %d photograph events", events)
	}
}

func TestAPhotographIsNeverPubliclyReadable(t *testing.T) {
	// Acceptance criterion 1, and the port's own promise. Checked against the store rather
	// than against the API, because the API is not where the object lives.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)

	ticket := h.uploadTicket(t, id)
	key := ticket["object_key"].(string)
	putBytes(t, ticket["upload_url"].(string), jpeg, "image/jpeg")
	if resp, body := h.attachPhoto(t, id, key); resp.StatusCode != http.StatusCreated {
		t.Fatalf("attach returned %d: %v", resp.StatusCode, body)
	}

	resp, body := h.call(t, http.MethodGet, "/v1/patients/"+id+"/photo", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
	signed := body["url"].(string)

	// The same URL with the signature removed is refused.
	unsigned := signed[:strings.Index(signed, "?")]
	plain, err := http.Get(unsigned) //nolint:gosec,noctx // the URL under test
	if err != nil {
		t.Fatal(err)
	}
	_ = plain.Body.Close()
	if plain.StatusCode == http.StatusOK {
		t.Error("the photograph is readable without a signature")
	}
}

func TestASignedPhotoURLExpiresWithinTheCap(t *testing.T) {
	// Acceptance criterion 2. The cap is the policy; a caller may ask for less.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)
	ticket := h.uploadTicket(t, id)
	putBytes(t, ticket["upload_url"].(string), jpeg, "image/jpeg")
	if resp, _ := h.attachPhoto(t, id, ticket["object_key"].(string)); resp.StatusCode != http.StatusCreated {
		t.Fatal("attach failed")
	}

	_, body := h.call(t, http.MethodGet, "/v1/patients/"+id+"/photo?ttl_seconds=60", nil)
	if !strings.Contains(body["url"].(string), "X-Amz-Expires=60") {
		t.Errorf("a shorter ttl was ignored: %v", body["url"])
	}
	// And a longer one is not honoured.
	_, capped := h.call(t, http.MethodGet, "/v1/patients/"+id+"/photo?ttl_seconds=86400", nil)
	if !strings.Contains(capped["url"].(string), "X-Amz-Expires=900") {
		t.Errorf("a longer ttl was honoured: %v", capped["url"])
	}
}

func TestAnUnuploadedPhotographIsRefusedRatherThanRecorded(t *testing.T) {
	// The row would otherwise point at an object nobody can render, and the failure would
	// surface as a broken image on a clinician's screen weeks later.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)
	ticket := h.uploadTicket(t, id)

	resp, body := h.attachPhoto(t, id, ticket["object_key"].(string))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
	var rows int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.patient_photo`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("a photograph that was never uploaded was recorded")
	}
}

func TestAKeyFromAnotherPatientIsRefused(t *testing.T) {
	h := newAPI(t)
	first := h.registerAs(t, func(map[string]any) {})
	second := h.registerAs(t, func(body map[string]any) {
		body["name_en"] = "Abdul Karim"
		body["phone_primary"] = "01812345678"
		body["consent_reference"] = "consent_2026_0002"
	})

	ticket := h.uploadTicket(t, first["id"].(string))
	putBytes(t, ticket["upload_url"].(string), jpeg, "image/jpeg")

	// Attaching the first patient's object to the second patient's record.
	resp, body := h.attachPhoto(t, second["id"].(string), ticket["object_key"].(string))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("one patient's photograph was attached to another: %d %v", resp.StatusCode, body)
	}
}

func TestAFileThatIsNotAnImageIsRefusedBeforeAURLIsIssued(t *testing.T) {
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	resp, body := h.call(t, http.MethodPost,
		"/v1/patients/"+created["id"].(string)+"/photo/upload-url",
		map[string]any{"content_type": "application/pdf"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d: %v", resp.StatusCode, body)
	}
}

func TestReplacingAPhotographKeepsTheOldOne(t *testing.T) {
	// A chart printed last month showing a different face has to be explicable.
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	id := created["id"].(string)

	first := h.uploadTicket(t, id)
	putBytes(t, first["upload_url"].(string), jpeg, "image/jpeg")
	if resp, _ := h.attachPhoto(t, id, first["object_key"].(string)); resp.StatusCode != http.StatusCreated {
		t.Fatal("the first attach failed")
	}

	replacement := append([]byte{0xff, 0xd8}, []byte(strings.Repeat("newer", 200))...)
	second := h.uploadTicket(t, id)
	putBytes(t, second["upload_url"].(string), replacement, "image/jpeg")
	if resp, body := h.attachPhoto(t, id, second["object_key"].(string)); resp.StatusCode != http.StatusCreated {
		t.Fatalf("the replacement failed: %d %v", resp.StatusCode, body)
	}

	// Two rows, one live, the newer pointing at the older.
	var total, live int
	if err := h.SQL.QueryRow(`SELECT count(*) FROM core.patient_photo`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM core.patient_photo WHERE replaced_at IS NULL`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if total != 2 || live != 1 {
		t.Errorf("%d photographs, %d live", total, live)
	}
	var replaces string
	if err := h.SQL.QueryRow(`
		SELECT p.object_key FROM core.patient_photo n
		  JOIN core.patient_photo p ON p.id = n.replaces_id
		 WHERE n.replaced_at IS NULL`).Scan(&replaces); err != nil {
		t.Fatal(err)
	}
	if replaces != first["object_key"] {
		t.Errorf("the replacement points at %q", replaces)
	}

	// And the current URL serves the newer bytes.
	_, body := h.call(t, http.MethodGet, "/v1/patients/"+id+"/photo", nil)
	if got := fetch(t, body["url"].(string)); !bytes.Equal(got, replacement) {
		t.Error("the current photograph is not the replacement")
	}

	// The old object is still there: the event names it, and a chart printed then can
	// still be explained.
	var events int
	if err := h.SQL.QueryRow(
		`SELECT count(*) FROM ledger.event WHERE event_type = 'PATIENT_PHOTO_CAPTURED'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Errorf("the ledger holds %d photograph events", events)
	}
}

func TestAPatientWithNoPhotographIsNotFound(t *testing.T) {
	h := newAPI(t)
	created := h.registerAs(t, func(map[string]any) {})
	resp, _ := h.call(t, http.MethodGet, "/v1/patients/"+created["id"].(string)+"/photo", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestPhotographBytesNeverReachTheDatabase(t *testing.T) {
	// D-01. A photograph in the database is a photograph in every backup, every replica
	// and every pg_dump an engineer takes to debug something.
	h := newAPI(t)
	if _, err := h.SQL.Exec(`SELECT core.assert_photos_are_referenced_not_stored()`); err != nil {
		t.Errorf("assert_photos_are_referenced_not_stored: %v", err)
	}
	var binary int
	if err := h.SQL.QueryRow(`
		SELECT count(*) FROM information_schema.columns
		 WHERE table_schema = 'core' AND table_name = 'patient_photo'
		   AND data_type = 'bytea' AND column_name <> 'sha256'`).Scan(&binary); err != nil {
		t.Fatal(err)
	}
	if binary != 0 {
		t.Errorf("core.patient_photo has %d binary columns besides the digest", binary)
	}
}

func fetch(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec,noctx // the URL under test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fetching the photograph returned %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	return body
}
