package settings

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"explo/src/web/backend/app"
)

// flagsSettings points a Settings at a throwaway .env seeded with the given
// contents.
func flagsSettings(t *testing.T, env string) (*Settings, string) {
	t.Helper()

	envPath := filepath.Join(t.TempDir(), ".env")
	if env != "" {
		if err := os.WriteFile(envPath, []byte(env), 0o644); err != nil {
			t.Fatalf("seeding env: %v", err)
		}
	}

	return NewSettings(app.Config{WebEnvPath: envPath}), envPath
}

// flagsFor reads back one playlist's FLAGS value.
func flagsFor(t *testing.T, s *Settings, envPath, key string) string {
	t.Helper()

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("reading env: %v", err)
	}
	return s.ParseEnvText(string(data))[key]
}

func postJSON(t *testing.T, handler http.HandlerFunc, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling body: %v", err)
	}

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload)))
	return rec
}

// Turning the toggle on has to write the flag the runner actually reads.
func TestSaveLocalOnly_AddsAndRemovesTheFlag(t *testing.T) {
	s, envPath := flagsSettings(t, "DAILY_JAMS_FLAGS=--playlist daily-jams\n")

	if rec := postJSON(t, s.HandleSaveLocalOnly, map[string]any{
		"id": "daily-jams", "local_only": true,
	}); rec.Code != http.StatusOK {
		t.Fatalf("enabling = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	flags := flagsFor(t, s, envPath, "DAILY_JAMS_FLAGS")
	if !strings.Contains(flags, "--download-mode=skip") {
		t.Errorf("flags = %q, want the local-only flag", flags)
	}
	if !strings.Contains(flags, "--playlist daily-jams") {
		t.Errorf("flags = %q, lost the existing flags", flags)
	}

	if rec := postJSON(t, s.HandleSaveLocalOnly, map[string]any{
		"id": "daily-jams", "local_only": false,
	}); rec.Code != http.StatusOK {
		t.Fatalf("disabling = %d, want 200", rec.Code)
	}

	flags = flagsFor(t, s, envPath, "DAILY_JAMS_FLAGS")
	if strings.Contains(flags, "--download-mode=skip") {
		t.Errorf("flags = %q, want the local-only flag gone", flags)
	}
	if !strings.Contains(flags, "--playlist daily-jams") {
		t.Errorf("flags = %q, lost the existing flags on removal", flags)
	}
	if strings.Contains(flags, "  ") {
		t.Errorf("flags = %q, removal left a double space", flags)
	}
}

// The UI can fire the same request twice; neither direction may compound.
func TestSaveLocalOnly_IsIdempotent(t *testing.T) {
	s, envPath := flagsSettings(t, "DAILY_JAMS_FLAGS=--playlist daily-jams\n")

	for i := 0; i < 3; i++ {
		postJSON(t, s.HandleSaveLocalOnly, map[string]any{"id": "daily-jams", "local_only": true})
	}
	if got := strings.Count(flagsFor(t, s, envPath, "DAILY_JAMS_FLAGS"), "--download-mode=skip"); got != 1 {
		t.Errorf("flag appears %d times after repeated enables, want 1", got)
	}

	for i := 0; i < 3; i++ {
		postJSON(t, s.HandleSaveLocalOnly, map[string]any{"id": "daily-jams", "local_only": false})
	}
	if flags := flagsFor(t, s, envPath, "DAILY_JAMS_FLAGS"); strings.Contains(flags, "--download-mode=skip") {
		t.Errorf("flags = %q, want the flag gone after repeated disables", flags)
	}
}

// Setting one playlist to local-only must leave the others downloading -- the
// whole point is that it is per playlist.
func TestSaveLocalOnly_LeavesOtherPlaylistsAlone(t *testing.T) {
	s, envPath := flagsSettings(t, "DAILY_JAMS_FLAGS=--playlist daily-jams\nWEEKLY_JAMS_FLAGS=--playlist weekly-jams\n")

	postJSON(t, s.HandleSaveLocalOnly, map[string]any{"id": "daily-jams", "local_only": true})

	if flags := flagsFor(t, s, envPath, "WEEKLY_JAMS_FLAGS"); strings.Contains(flags, "--download-mode=skip") {
		t.Errorf("weekly-jams = %q, want it untouched", flags)
	}
}

// It must not disturb the toggle that sits next to it in the same menu.
func TestSaveLocalOnly_PreservesTheReplaceFlag(t *testing.T) {
	s, envPath := flagsSettings(t, "DAILY_JAMS_FLAGS=--playlist daily-jams --replace-playlist=false\n")

	postJSON(t, s.HandleSaveLocalOnly, map[string]any{"id": "daily-jams", "local_only": true})

	flags := flagsFor(t, s, envPath, "DAILY_JAMS_FLAGS")
	if !strings.Contains(flags, "--replace-playlist=false") {
		t.Errorf("flags = %q, lost the replace setting", flags)
	}
	if !strings.Contains(flags, "--download-mode=skip") {
		t.Errorf("flags = %q, want the local-only flag", flags)
	}
}

// Saving a schedule rewrites FLAGS wholesale from a default and re-adds only
// the flags it knows about. Leaving the new one off that list would silently
// turn local-only back off the next time the schedule was edited.
func TestSaveSchedule_PreservesLocalOnly(t *testing.T) {
	s, envPath := flagsSettings(t, "DAILY_JAMS_FLAGS=--playlist daily-jams --download-mode=skip\n")

	rec := postJSON(t, s.HandleSaveSchedule, map[string]any{
		"id": "daily-jams", "enabled": true, "day": -1, "hour": 3, "minute": 30,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("saving schedule = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	if flags := flagsFor(t, s, envPath, "DAILY_JAMS_FLAGS"); !strings.Contains(flags, "--download-mode=skip") {
		t.Errorf("flags = %q, editing the schedule dropped local-only", flags)
	}
}

// Custom playlists get their env prefix from their name, not their id.
func TestSaveLocalOnly_SupportsCustomPlaylists(t *testing.T) {
	s, envPath := flagsSettings(t, "")

	rec := postJSON(t, s.HandleSaveLocalOnly, map[string]any{
		"id": "custom-abc123", "name": "Road Trip", "local_only": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("custom playlist = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("reading env: %v", err)
	}
	if !bytes.Contains(data, []byte("--download-mode=skip")) {
		t.Errorf("custom playlist flags were not written:\n%s", data)
	}
}

// An unrecognised playlist is the caller's mistake, not a server fault.
func TestSaveLocalOnly_RejectsAnUnknownPlaylist(t *testing.T) {
	s, _ := flagsSettings(t, "")

	rec := postJSON(t, s.HandleSaveLocalOnly, map[string]any{"id": "nonsense", "local_only": true})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown playlist = %d, want 400", rec.Code)
	}
}

// The refactor that introduced the shared helper must not have changed how the
// neighbouring toggle behaves.
func TestSaveReplacePlaylist_StillWorks(t *testing.T) {
	s, envPath := flagsSettings(t, "DAILY_JAMS_FLAGS=--playlist daily-jams\n")

	postJSON(t, s.HandleSaveReplacePlaylist, map[string]any{"id": "daily-jams", "replace": false})
	if flags := flagsFor(t, s, envPath, "DAILY_JAMS_FLAGS"); !strings.Contains(flags, "--replace-playlist=false") {
		t.Errorf("flags = %q, want the replace flag added when replace is off", flags)
	}

	postJSON(t, s.HandleSaveReplacePlaylist, map[string]any{"id": "daily-jams", "replace": true})
	if flags := flagsFor(t, s, envPath, "DAILY_JAMS_FLAGS"); strings.Contains(flags, "--replace-playlist=false") {
		t.Errorf("flags = %q, want the replace flag gone when replace is on", flags)
	}
}
