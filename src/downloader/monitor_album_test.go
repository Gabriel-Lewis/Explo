package downloader

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	cfg "explo/src/config"
	"explo/src/models"
)

// fakeMigrator stands in for a downloader that fetches whole releases, draining
// a fixed number of files per pass so a test can control how progress unfolds.
type fakeMigrator struct {
	remaining  int
	perPass    int
	calls      int
	lastDest   string
	lastSource string
}

func (f *fakeMigrator) QueryTrack(*models.Track) error { return nil }
func (f *fakeMigrator) GetTrack(*models.Track) error   { return nil }

func (f *fakeMigrator) GetDownloadStatus([]*models.Track) (map[string]FileStatus, error) {
	return nil, nil
}
func (f *fakeMigrator) GetConf() (MonitorConfig, error)  { return MonitorConfig{}, nil }
func (f *fakeMigrator) Cleanup(models.Track, string) error { return nil }

// MoveDownload moves the recommended track for real, as a downloader would, so
// the monitor has a landing directory to hand the rest of the release.
func (f *fakeMigrator) MoveDownload(srcDir, destDir, trackPath string, track *models.Track) (string, error) {
	return moveTrack(filepath.Join(srcDir, trackPath, track.File), destDir, track, "", false)
}

func (f *fakeMigrator) MoveAlbumSiblings(trackDir, destDir string, track *models.Track) int {
	f.calls++
	f.lastSource, f.lastDest = trackDir, destDir

	f.remaining -= f.perPass
	if f.remaining < 0 {
		f.remaining = 0
	}
	return f.remaining
}

func newClient(t *testing.T, m *fakeMigrator) *DownloadClient {
	t.Helper()

	return &DownloadClient{
		Cfg:         &cfg.DownloadConfig{},
		Downloaders: []Downloader{m},
	}
}

// A release whose files keep arriving must be revisited until none are left,
// and its download directory cleaned up once it is done.
func TestMigratePendingAlbums_RetriesUntilComplete(t *testing.T) {
	migrator := &fakeMigrator{remaining: 3, perPass: 1}
	client := newClient(t, migrator)

	trackDir := t.TempDir()
	monCfg := MonitorConfig{StallDuration: time.Hour, MaxDuration: 24 * time.Hour}

	pending := map[string]*pendingAlbum{}
	client.trackAlbum(pending, "key", &pendingAlbum{
		track:      &models.Track{Album: "When We All Fall Asleep"},
		trackDir:   trackDir,
		destDir:    t.TempDir(),
		startedAt:  time.Now(),
		lastChange: time.Now(),
	}, monCfg)

	if len(pending) != 1 {
		t.Fatal("release with outstanding files was not recorded for retry")
	}

	for i := 0; i < 3; i++ {
		client.migratePendingAlbums(pending, monCfg, time.Now())
	}

	if len(pending) != 0 {
		t.Errorf("release still pending after every file arrived (%d passes)", migrator.calls)
	}
	if _, err := os.Stat(trackDir); !os.IsNotExist(err) {
		t.Errorf("emptied release directory was not cleaned up: %v", err)
	}
}

// Progress must reset the clock, or a slow release is abandoned mid-transfer.
func TestMigratePendingAlbums_ProgressExtendsTheDeadline(t *testing.T) {
	migrator := &fakeMigrator{remaining: 10, perPass: 1}
	client := newClient(t, migrator)

	start := time.Now()
	monCfg := MonitorConfig{StallDuration: time.Minute, MaxDuration: 24 * time.Hour}

	album := &pendingAlbum{
		track:      &models.Track{Album: "When We All Fall Asleep"},
		trackDir:   t.TempDir(),
		destDir:    t.TempDir(),
		startedAt:  start,
		lastChange: start,
	}
	pending := map[string]*pendingAlbum{}
	client.trackAlbum(pending, "key", album, monCfg)

	// Well past the deadline, but a file lands on every pass.
	client.migratePendingAlbums(pending, monCfg, start.Add(10*time.Minute))

	if len(pending) != 1 {
		t.Fatal("a release still receiving files was abandoned")
	}
	if !album.lastChange.After(start) {
		t.Error("progress did not reset the deadline")
	}
}

// A peer that goes away must not hold the monitor open forever.
func TestMigratePendingAlbums_GivesUpWhenStalled(t *testing.T) {
	migrator := &fakeMigrator{remaining: 5, perPass: 0}
	client := newClient(t, migrator)

	start := time.Now()
	trackDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(trackDir, "half.flac"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seeding stalled file: %v", err)
	}

	monCfg := MonitorConfig{StallDuration: time.Minute, MaxDuration: 24 * time.Hour}

	pending := map[string]*pendingAlbum{}
	client.trackAlbum(pending, "key", &pendingAlbum{
		track:      &models.Track{Album: "When We All Fall Asleep"},
		trackDir:   trackDir,
		destDir:    t.TempDir(),
		startedAt:  start,
		lastChange: start,
	}, monCfg)

	client.migratePendingAlbums(pending, monCfg, start.Add(30*time.Second))
	if len(pending) != 1 {
		t.Fatal("release abandoned before the deadline")
	}

	client.migratePendingAlbums(pending, monCfg, start.Add(2*time.Minute))
	if len(pending) != 0 {
		t.Error("a stalled release was never abandoned; the monitor would never finish")
	}
	if _, err := os.Stat(filepath.Join(trackDir, "half.flac")); err != nil {
		t.Errorf("abandoned files should be left in place, not deleted: %v", err)
	}
}

// A release that keeps trickling in resets the stall clock every pass, so only
// the overall cap stops it from holding the monitor open indefinitely.
func TestMigratePendingAlbums_GivesUpPastTheMaxDuration(t *testing.T) {
	migrator := &fakeMigrator{remaining: 100, perPass: 1}
	client := newClient(t, migrator)

	start := time.Now()
	monCfg := MonitorConfig{StallDuration: time.Hour, MaxDuration: time.Hour}

	pending := map[string]*pendingAlbum{}
	client.trackAlbum(pending, "key", &pendingAlbum{
		track:      &models.Track{Album: "When We All Fall Asleep"},
		trackDir:   t.TempDir(),
		destDir:    t.TempDir(),
		startedAt:  start,
		lastChange: start,
	}, monCfg)

	client.migratePendingAlbums(pending, monCfg, start.Add(30*time.Minute))
	if len(pending) != 1 {
		t.Fatal("release abandoned before the max duration")
	}

	// Still making progress, but past the cap.
	client.migratePendingAlbums(pending, monCfg, start.Add(2*time.Hour))
	if len(pending) != 0 {
		t.Error("a release still trickling in was never abandoned at the max duration")
	}
}

// A single-track download has no siblings, so it must never be recorded.
func TestTrackAlbum_IgnoresReleasesWithNothingLeft(t *testing.T) {
	client := newClient(t, &fakeMigrator{remaining: 0, perPass: 0})

	pending := map[string]*pendingAlbum{}
	client.trackAlbum(pending, "key", &pendingAlbum{
		track:    &models.Track{Album: "Single"},
		trackDir: t.TempDir(),
		destDir:  t.TempDir(),
	}, MonitorConfig{})

	if len(pending) != 0 {
		t.Error("a release with no outstanding files was recorded for retry")
	}
}

// monitorStub drives MonitorDownloads: the tracked file is reported complete
// immediately, while the release behind it still has files in flight.
type monitorStub struct {
	fakeMigrator
	cfg MonitorConfig
}

func (m *monitorStub) GetConf() (MonitorConfig, error) { return m.cfg, nil }

func (m *monitorStub) GetDownloadStatus(tracks []*models.Track) (map[string]FileStatus, error) {
	statuses := make(map[string]FileStatus, len(tracks))
	for _, track := range tracks {
		statuses[track.ID] = FileStatus{ID: "1", Filename: track.File, State: "Completed, Succeeded", PercentComplete: 100}
	}
	return statuses, nil
}

// The regression in full: MonitorDownloads must not finish while a release
// still has files coming. Before this, it migrated once at the moment the
// recommended track landed and then returned, stranding the rest of the album.
func TestMonitorDownloads_WaitsForTheRestOfTheRelease(t *testing.T) {
	fromDir := t.TempDir()
	toDir := t.TempDir()

	releaseDir := filepath.Join(fromDir, "release")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		t.Fatalf("creating release dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseDir, "primary.flac"), []byte("primary"), 0o644); err != nil {
		t.Fatalf("seeding primary: %v", err)
	}

	stub := &monitorStub{
		fakeMigrator: fakeMigrator{remaining: 3, perPass: 1},
		cfg: MonitorConfig{
			CheckInterval:   time.Millisecond,
			StallDuration:   time.Hour,
			MaxDuration:     24 * time.Hour,
			MigrateDownload: true,
			FromDir:         fromDir,
			ToDir:           toDir,
			Service:         "slskd",
		},
	}

	client := &DownloadClient{
		Cfg:         &cfg.DownloadConfig{},
		Downloaders: []Downloader{stub},
	}

	track := &models.Track{
		ID:         "track-1",
		Album:      "When We All Fall Asleep",
		CleanTitle: "Bad Guy",
		File:       `release\primary.flac`,
	}

	done := make(chan error, 1)
	go func() { done <- client.MonitorDownloads([]*models.Track{track}, stub) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("MonitorDownloads() = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MonitorDownloads never returned")
	}

	// One pass drains one file, so it must have come back for the other two
	// rather than migrating once and returning.
	if stub.calls < 3 {
		t.Errorf("migrator called %d times; the monitor stopped before the release finished", stub.calls)
	}
	if stub.remaining != 0 {
		t.Errorf("%d files were left behind", stub.remaining)
	}
}
