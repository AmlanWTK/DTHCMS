package prescription

// A test file may reach across a boundary: it is not shipped, and a test-only import
// cannot become a production dependency without a non-test file importing it too — which
// is checked.
import "example.com/backend/internal/records"

var _ = records.Ingest
