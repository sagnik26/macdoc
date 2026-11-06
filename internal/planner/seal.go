package planner

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/sagnik26/macdoc/internal/fsread"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/safety"
	"github.com/sagnik26/macdoc/internal/store"
)

const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
	// maxPlanFile bounds what Load reads.
	maxPlanFile int64 = 4 << 20
	// idChars avoid look-alikes (no I, O, 0, 1): the ID's code is typed to
	// confirm an apply.
	idChars   = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	idLen     = 4
	idRetries = 16
)

// planIDRe is a plan ID: what Load accepts, so an ID can never name a path.
var planIDRe = regexp.MustCompile(`^P-[A-HJ-NP-Z2-9]{4}$`)

// Verification errors.
var (
	ErrUnknown  = errors.New("no such plan")
	ErrTampered = errors.New("the plan doesn't match its seal")
	ErrExpired  = errors.New("the plan has expired")
	ErrApplied  = errors.New("the plan was already applied")
	ErrInvalid  = errors.New("the plan contains an item macdoc wouldn't plan")
)

// Dir is the plans folder next to the store.
func Dir(storePath string) string {
	return filepath.Join(filepath.Dir(storePath), "plans")
}

// ValidID reports whether id is a well-formed plan ID ("P-7F3K").
func ValidID(id string) bool { return planIDRe.MatchString(id) }

// Hash is the plan's seal: SHA-256 over its canonical JSON (encoding/json of
// the struct, fields in declaration order, with SHA256 empty).
func Hash(p model.Plan) (string, error) {
	p.SHA256 = ""
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Save assigns the plan an unused ID, seals it, writes it to dir as
// P-XXXX.json (a new file, never overwriting one) and records its hash in
// the store. The store row is written last: a plan without one never
// verifies.
func Save(st *store.Store, dir string, p *model.Plan) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("plans folder: %w", err)
	}
	for range idRetries {
		id, err := NewID()
		if err != nil {
			return err
		}
		taken, err := st.HasPlan(id)
		if err != nil {
			return err
		}
		if taken {
			continue
		}
		p.ID = id
		if p.SHA256, err = Hash(*p); err != nil {
			return err
		}
		b, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return err
		}
		if err := WriteNew(filepath.Join(dir, id+".json"), append(b, '\n')); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return err
		}
		return st.AddPlan(store.PlanRecord{
			ID: id, SHA256: p.SHA256, ScanID: p.ScanID, Origin: p.Origin,
			CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt, Items: len(p.Items), TotalBytes: p.TotalBytes,
		})
	}
	return errors.New("couldn't find an unused plan ID")
}

// NewID returns a random plan ID: "P-" and four characters that avoid
// look-alikes, since the person types them to confirm.
func NewID() (string, error) {
	b := make([]byte, idLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(idChars))))
		if err != nil {
			return "", err
		}
		b[i] = idChars[n.Int64()]
	}
	return "P-" + string(b), nil
}

// WriteNew creates path (failing if it exists), writes b and syncs it.
func WriteNew(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode) //nolint:gosec // G304: path is the plans folder plus a generated ID.
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Load reads plan id from dir. Unknown fields or trailing data are errors,
// so nothing can be smuggled past the seal.
func Load(dir, id string) (model.Plan, error) {
	var p model.Plan
	if !ValidID(id) {
		return p, fmt.Errorf("%q isn't a plan ID (they look like P-7F3K)", id)
	}
	b, err := fsread.SmallFile(filepath.Join(dir, id+".json"), maxPlanFile)
	if errors.Is(err, os.ErrNotExist) {
		return p, fmt.Errorf("%w: %s", ErrUnknown, id)
	}
	if err != nil {
		return p, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("%w: %s can't be read: %w", ErrTampered, id, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return p, fmt.Errorf("%w: %s has extra data after the plan", ErrTampered, id)
	}
	return p, nil
}

// Verify is the gate apply must pass (docs/SAFETY_MODEL.md §4.3 step 1): the
// plan's content hashes to its seal, the store holds the same seal for the
// same ID and scan, it hasn't expired or been applied, and every item is one
// the planner could have produced (its recipe, method, command and
// allowlist still agree). It reads nothing but the plan, the store row and
// the catalog.
func Verify(p model.Plan, rec store.PlanRecord, cat *recipes.Catalog, pol *safety.Policy, now time.Time) error {
	h, err := Hash(p)
	if err != nil {
		return err
	}
	if p.SHA256 == "" || h != p.SHA256 || rec.ID != p.ID || rec.SHA256 != p.SHA256 || rec.ScanID != p.ScanID ||
		!rec.ExpiresAt.Equal(p.ExpiresAt) || p.SchemaVersion != model.SchemaVersion {
		return ErrTampered
	}
	if rec.Status == store.PlanApplied {
		return ErrApplied
	}
	if !now.Before(p.ExpiresAt) {
		return ErrExpired
	}
	return validateItems(p, cat, pol)
}

// validateItems re-checks every item against the catalog and policy.
func validateItems(p model.Plan, cat *recipes.Catalog, pol *safety.Policy) error {
	if len(p.Items) == 0 {
		return fmt.Errorf("%w: it has no items", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, it := range p.Items {
		bad := func(why string) error { return fmt.Errorf("%w: %s %s", ErrInvalid, it.OpportunityID, why) }
		if seen[it.OpportunityID] {
			return bad("appears twice")
		}
		seen[it.OpportunityID] = true
		r, ok := cat.Get(it.RecipeID)
		switch {
		case !ok:
			return bad("has an unknown recipe")
		case r.Method != it.Method:
			return bad("has the wrong method for its recipe")
		case it.Risk == model.RiskAdvise || it.Risk == model.RiskBlocked || tierRank(it.Risk) < tierRank(r.Risk):
			return bad("has a risk its recipe doesn't allow")
		}
		switch it.Method {
		case model.MethodNative:
			if !recipes.MatchesNative(r, it.Argv) {
				return bad("has a command its recipe doesn't allow")
			}
		case model.MethodQuarantine:
			if len(it.Argv) != 0 {
				return bad("is a move with a command attached")
			}
			if err := pol.CheckTarget(it.Target, r.Allow); err != nil {
				return bad("targets a path the safety rules refuse")
			}
		default:
			return bad("has an unknown method")
		}
	}
	return nil
}

// Status is a plan's state for display.
type Status string

// The states a plan can be in.
const (
	StatusPending Status = "pending"
	StatusExpired Status = "expired"
	StatusApplied Status = "applied"
	StatusInvalid Status = "invalid" // fails Verify for another reason
)

// StatusOf classifies a Verify result.
func StatusOf(err error) Status {
	switch {
	case err == nil:
		return StatusPending
	case errors.Is(err, ErrExpired):
		return StatusExpired
	case errors.Is(err, ErrApplied):
		return StatusApplied
	}
	return StatusInvalid
}
