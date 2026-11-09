// Package quarantine reads macdoc's quarantine (docs/SAFETY_MODEL.md §4.6):
// folders that apply moved aside instead of deleting, restorable until they
// expire. It only reads; moving, restoring and purging are done by
// internal/executor.
//
// Layout: <state>/quarantine/Q-XXXX/manifest.json and
// <state>/quarantine/Q-XXXX/payload/<name>. Each entry also has a row in the
// store, so a folder macdoc didn't create is never treated as an entry.
package quarantine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/sagnik26/macdoc/internal/fsread"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/store"
)

const (
	// Dir is the quarantine folder inside the state directory.
	Dir = "quarantine"
	// ManifestName is the file in an entry that describes it.
	ManifestName = "manifest.json"
	// Payload is the folder in an entry that holds the moved item.
	Payload = "payload"
	// TTL is how long an entry stays before it's reported as ready
	// to purge. Nothing is purged automatically.
	TTL = 7 * 24 * time.Hour
	// Schema is the manifest format.
	Schema = "1"
	most   = 1 << 20
)

// pattern is a quarantine ID, the same alphabet as plan IDs.
var pattern = regexp.MustCompile(`^Q-[A-HJ-NP-Z2-9]{4}$`)

// ValidID reports whether id is a well-formed quarantine ID ("Q-91XZ").
func ValidID(id string) bool { return pattern.MatchString(id) }

// Root is the quarantine folder next to the store.
func Root(storePath string) string {
	return filepath.Join(filepath.Dir(storePath), Dir)
}

// Status values a manifest may hold. Restored and purged entries have no
// folder any more.
const (
	Pending = "pending" // written before the move
	Done    = "done"    // written after the move
)

// Manifest describes one entry. Apply writes it before moving anything
// (pending) and again after (done), so a crash leaves a recoverable entry.
type Manifest struct {
	Schema      string            `json:"schema_version"`
	ID          string            `json:"id"`
	Plan        string            `json:"plan"`
	Item        string            `json:"item"`
	Recipe      string            `json:"recipe"`
	Title       string            `json:"title"`
	Original    string            `json:"original"` // where it came from, canonical
	Name        string            `json:"name"`     // payload/<name>
	Bytes       int64             `json:"bytes"`
	Source      model.Source      `json:"bytes_source"`
	Fingerprint model.Fingerprint `json:"fingerprint"`
	Created     time.Time         `json:"created_at"`
	Expires     time.Time         `json:"expires_at"`
	Status      string            `json:"status"`
}

// EntryDir is the folder of entry id under the quarantine root.
func EntryDir(root, id string) string { return filepath.Join(root, id) }

// ManifestPath is the manifest file of entry id.
func ManifestPath(root, id string) string { return filepath.Join(root, id, ManifestName) }

// PayloadPath is where the moved item sits inside its entry.
func PayloadPath(root string, m Manifest) string {
	return filepath.Join(root, m.ID, Payload, m.Name)
}

// ErrBadManifest means a manifest is missing, unreadable or inconsistent.
var ErrBadManifest = errors.New("quarantine manifest is invalid")

// Load reads entry id's manifest and checks it names itself and a
// plain file name.
func Load(root, id string) (Manifest, error) {
	var m Manifest
	if !ValidID(id) {
		return m, fmt.Errorf("%q isn't a quarantine ID (they look like Q-91XZ)", id)
	}
	b, err := fsread.Small(ManifestPath(root, id), most)
	if err != nil {
		return m, fmt.Errorf("%w: %s: %w", ErrBadManifest, id, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("%w: %s: %w", ErrBadManifest, id, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return m, fmt.Errorf("%w: %s has extra data", ErrBadManifest, id)
	}
	switch {
	case m.Schema != Schema, m.ID != id:
		return m, fmt.Errorf("%w: %s doesn't describe itself", ErrBadManifest, id)
	case !ValidName(m.Name):
		return m, fmt.Errorf("%w: %s has a bad payload name", ErrBadManifest, id)
	case !filepath.IsAbs(m.Original) || filepath.Base(m.Original) != m.Name:
		return m, fmt.Errorf("%w: %s has a bad original path", ErrBadManifest, id)
	case m.Status != Pending && m.Status != Done:
		return m, fmt.Errorf("%w: %s has an unknown status", ErrBadManifest, id)
	}
	return m, nil
}

// ValidName reports whether name is a single path component.
func ValidName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !nul(name)
}

func nul(s string) bool { return bytes.IndexByte([]byte(s), 0) >= 0 }

// State is an entry's condition as found on disk.
type State string

// The states of a quarantine entry, from what is on disk.
const (
	Held        State = "held"        // moved into quarantine; restorable
	Interrupted State = "interrupted" // apply stopped mid-move but the folder is here; restorable
	Unmoved     State = "not-moved"   // apply stopped before the move; the original was never touched
	Missing     State = "missing"     // macdoc's record says it's here but the folder is gone
	Damaged     State = "damaged"     // the manifest can't be trusted; never touched
)

// Entry is one quarantine entry with its record and state.
type Entry struct {
	Record   store.QuarantineRecord `json:"record"`
	Manifest *Manifest              `json:"manifest,omitempty"`
	State    State                  `json:"state"`
	Expired  bool                   `json:"expired"`
	Problem  string                 `json:"problem,omitempty"`
}

// Restorable reports whether restore can act on the entry.
func (e Entry) Restorable() bool {
	return e.State == Held || e.State == Interrupted
}

// Listing is every live entry plus folders macdoc has no record of.
type Listing struct {
	Entries []Entry  `json:"entries"`
	Unknown []string `json:"unknown"` // folder names in the quarantine root without a store row; never touched
}

// Held and ExpiredBytes total the restorable entries.
func (l Listing) Held() (n int64) {
	for _, e := range l.Entries {
		if e.Restorable() {
			n += e.Record.Bytes
		}
	}
	return n
}

// Expired returns the restorable entries past their expiry.
func (l Listing) Expired() []Entry {
	var out []Entry
	for _, e := range l.Entries {
		if e.Restorable() && e.Expired {
			out = append(out, e)
		}
	}
	return out
}

// List reads every pending or done entry from the store and checks it on
// disk. It never changes anything.
func List(st *store.Store, root string, now time.Time) (Listing, error) {
	recs, err := st.QuarantineEntries(store.QuarantinePending, store.QuarantineDone)
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Entries: []Entry{}, Unknown: []string{}}
	known := map[string]bool{}
	for _, r := range recs {
		known[r.ID] = true
		out.Entries = append(out.Entries, Inspect(root, r, now))
	}
	des, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	for _, de := range des {
		if !known[de.Name()] && de.Name() != ".metadata_never_index" {
			out.Unknown = append(out.Unknown, de.Name())
		}
	}
	sort.Strings(out.Unknown)
	return out, nil
}

// Inspect checks one recorded entry on disk.
func Inspect(root string, r store.QuarantineRecord, now time.Time) Entry {
	e := Entry{Record: r, Expired: !now.Before(r.ExpiresAt)}
	dir := EntryDir(root, r.ID)
	if fi, err := os.Lstat(dir); err != nil {
		e.State, e.Problem = Missing, "its folder is gone"
		if !errors.Is(err, os.ErrNotExist) {
			e.State, e.Problem = Damaged, err.Error()
		}
		return e
	} else if !fi.IsDir() {
		e.State, e.Problem = Damaged, "its folder isn't a folder"
		return e
	}
	m, err := Load(root, r.ID)
	if err != nil {
		e.State, e.Problem = Damaged, err.Error()
		return e
	}
	e.Manifest = &m
	if m.Original != r.Original || m.Plan != r.PlanID || !m.Expires.Equal(r.ExpiresAt) {
		e.State, e.Problem = Damaged, "the manifest doesn't match macdoc's record"
		return e
	}
	payload := PayloadPath(root, m)
	fi, err := os.Lstat(payload)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if m.Status == Done || r.Status == store.QuarantineDone {
			e.State, e.Problem = Missing, "the moved folder is gone"
		} else {
			e.State = Unmoved
		}
	case err != nil:
		e.State, e.Problem = Damaged, err.Error()
	case fi.Mode()&os.ModeSymlink != 0:
		e.State, e.Problem = Damaged, "the moved folder is now a symlink"
	case m.Status == Done && r.Status == store.QuarantineDone:
		e.State = Held
	default:
		e.State = Interrupted
	}
	return e
}
