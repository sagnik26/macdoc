package tests

import (
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// Spotify's cache folder is listed as CAUTION (it can hold offline songs), only
// that folder, and only while Spotify is closed.
func TestSpotifyCache(t *testing.T) {
	sp := w2Home + "/Library/Application Support/Spotify"
	fs, err := w2Run(t, "spotify.cache", recipes.Env{Nodes: nodesOf([]string{sp + "/PersistentCache"})})
	w2WantTargets(t, fs, err, sp+"/PersistentCache")
	for _, other := range []string{sp + "/Users", sp + "/prefs", sp + "/PersistentCache-old", sp + "/PersistentCache/Storage"} {
		fs, err = w2Run(t, "spotify.cache", recipes.Env{Nodes: nodesOf([]string{other})})
		w2WantNone(t, fs, err)
	}
	r := w2Recipe(t, "spotify.cache")
	if r.Risk != model.RiskCaution || r.Method != model.MethodQuarantine || len(r.Preconditions) != 1 || r.Preconditions[0] != "app_not_running:Spotify" {
		t.Errorf("spotify.cache = %s/%s %v", r.Risk, r.Method, r.Preconditions)
	}
	if r.Restore.Kind != model.RestoreRedownload || r.Restore.Note == "" {
		t.Errorf("restore = %+v", r.Restore)
	}
}
