package settings

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"explo/src/web/backend/app"
	"explo/src/web/backend/defs"
)

// newSettings points a Settings at a throwaway .env.
func newSettings(t *testing.T) (*Settings, string) {
	t.Helper()

	envPath := filepath.Join(t.TempDir(), ".env")

	return NewSettings(app.Config{WebEnvPath: envPath}), envPath
}

// postStep3 drives the wizard's downloader step with the given body.
func postStep3(t *testing.T, s *Settings, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshaling body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/ui/wizard/step3", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	s.HandleWizardStep3(rec, req)

	return rec
}

// The toggle is only useful if it reaches the .env the downloader reads.
func TestWizardStep3_PersistsAlbumMode(t *testing.T) {
	tests := []struct {
		name string
		on   bool
		want string
	}{
		{"enabled", true, "SLSKD_ALBUM_MODE=true"},
		{"disabled", false, "SLSKD_ALBUM_MODE=false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, envPath := newSettings(t)

			rec := postStep3(t, s, map[string]any{
				"download_services": []string{"slskd"},
				"slskd_album_mode":  tt.on,
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("step3 = %d (%s), want 200", rec.Code, rec.Body.String())
			}

			written, err := os.ReadFile(envPath)
			if err != nil {
				t.Fatalf("reading written env: %v", err)
			}
			if !bytes.Contains(written, []byte(tt.want)) {
				t.Errorf("written env does not contain %q:\n%s", tt.want, written)
			}
		})
	}
}

// AllConfigKeys is what /api/config reads back. Leaving the key out of it is
// silent: the wizard writes the toggle, then shows it as off next time it is
// opened, because the value never reaches the frontend.
func TestAlbumModeIsReadableByTheUI(t *testing.T) {
	if !slices.Contains(defs.AllConfigKeys, "SLSKD_ALBUM_MODE") {
		t.Error("SLSKD_ALBUM_MODE missing from AllConfigKeys; the wizard toggle would always read back as off")
	}
}
