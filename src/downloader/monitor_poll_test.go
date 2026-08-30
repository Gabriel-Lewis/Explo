package downloader

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	cfg "explo/src/config"
	"explo/src/models"
)

// pollStub drives MonitorDownloads with a scripted sequence of poll outcomes,
// so a test can interleave failures with successes.
type pollStub struct {
	cfg MonitorConfig
	// results is consumed one entry per poll; the last entry repeats.
	results []pollResult
	polls   int
}

type pollResult struct {
	err      error
	complete bool
}

func (p *pollStub) QueryTrack(*models.Track) error { return nil }
func (p *pollStub) GetTrack(*models.Track) error   { return nil }
func (p *pollStub) GetConf() (MonitorConfig, error) { return p.cfg, nil }
func (p *pollStub) Cleanup(models.Track, string) error { return nil }

func (p *pollStub) GetDownloadStatus(tracks []*models.Track) (map[string]FileStatus, error) {
	result := p.results[min(p.polls, len(p.results)-1)]
	p.polls++

	if result.err != nil {
		return nil, result.err
	}

	statuses := make(map[string]FileStatus, len(tracks))
	for _, track := range tracks {
		if result.complete {
			statuses[track.File] = FileStatus{ID: "1", State: "Completed, Succeeded", PercentComplete: 100}
			continue
		}
		statuses[track.File] = FileStatus{ID: "1", State: "InProgress", BytesTransferred: p.polls, BytesRemaining: 100}
	}
	return statuses, nil
}

func runMonitor(t *testing.T, stub *pollStub, track *models.Track) error {
	t.Helper()

	client := &DownloadClient{Cfg: &cfg.DownloadConfig{}}

	done := make(chan error, 1)
	go func() { done <- client.MonitorDownloads([]*models.Track{track}, stub) }()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("MonitorDownloads never returned")
		return nil
	}
}

func monitorCfg() MonitorConfig {
	return MonitorConfig{
		CheckInterval:   time.Millisecond,
		MonitorDuration: time.Hour,
		Service:         "slskd",
	}
}

// A blip must not cost the playlist. Tracks that never reach Present are
// dropped before the playlist is built, so aborting the monitor on one failed
// poll used to throw away every download still in flight.
func TestMonitorDownloads_SurvivesATransientPollFailure(t *testing.T) {
	stub := &pollStub{
		cfg: monitorCfg(),
		results: []pollResult{
			{err: fmt.Errorf("connection refused")},
			{complete: true},
		},
	}

	track := &models.Track{ID: "track-1", File: "song.flac"}

	if err := runMonitor(t, stub, track); err != nil {
		t.Fatalf("MonitorDownloads() = %v, want nil after recovering", err)
	}
	if !track.Present {
		t.Error("track was abandoned after a single failed poll; it would be dropped from the playlist")
	}
}

// Recovery must reset the budget, or a long run accumulates unrelated blips
// until it trips the limit.
func TestMonitorDownloads_ResetsTheFailureBudgetOnSuccess(t *testing.T) {
	stub := &pollStub{
		cfg: monitorCfg(),
		results: []pollResult{
			{err: fmt.Errorf("blip")},
			{err: fmt.Errorf("blip")},
			{}, // recovered, still downloading
			{err: fmt.Errorf("blip")},
			{err: fmt.Errorf("blip")},
			{complete: true},
		},
	}

	track := &models.Track{ID: "track-1", File: "song.flac"}

	if err := runMonitor(t, stub, track); err != nil {
		t.Fatalf("MonitorDownloads() = %v, want nil -- four failures spread around a success is not four in a row", err)
	}
	if !track.Present {
		t.Error("track never completed")
	}
}

// A service that is genuinely gone must still end the run rather than spin.
func TestMonitorDownloads_GivesUpOnRepeatedPollFailures(t *testing.T) {
	stub := &pollStub{
		cfg:     monitorCfg(),
		results: []pollResult{{err: fmt.Errorf("connection refused")}},
	}

	err := runMonitor(t, stub, &models.Track{ID: "track-1", File: "song.flac"})
	if err == nil {
		t.Fatal("MonitorDownloads() = nil, want an error once the service stays unreachable")
	}
	if stub.polls != maxPollFailures {
		t.Errorf("polled %d times, want %d before giving up", stub.polls, maxPollFailures)
	}
}

// An empty status map means slskd has not surfaced the transfers yet. Treating
// that as an error aborted the whole run one minute after queueing.
func TestMonitorDownloads_EmptyStatusIsNotAFailure(t *testing.T) {
	stub := &pollStub{
		cfg:     monitorCfg(),
		results: []pollResult{{}, {complete: true}},
	}

	// No tracks: GetDownloadStatus returns an empty map, exactly as slskd does
	// before any transfer appears.
	track := &models.Track{ID: "track-1", File: "song.flac"}
	if err := runMonitor(t, stub, track); err != nil {
		t.Fatalf("MonitorDownloads() = %v, want nil", err)
	}
	if !track.Present {
		t.Error("track never completed")
	}
}

// The slskd side of the same bug: an empty transfer list is a normal state a
// minute after queueing, not a failure to report upward.
func TestSlskdGetDownloadStatus_EmptyListIsNotAnError(t *testing.T) {
	client := albumClient(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`[]`)); err != nil {
			t.Errorf("fake slskd failed to write response: %v", err)
		}
	})

	statuses, err := client.GetDownloadStatus([]*models.Track{{File: "song.flac", MainArtistID: "peer1"}})
	if err != nil {
		t.Fatalf("GetDownloadStatus() = %v, want nil -- nothing queued yet is not a failure", err)
	}
	if len(statuses) != 0 {
		t.Errorf("got %d statuses, want none", len(statuses))
	}
}

// A genuinely broken request must still be reported.
func TestSlskdGetDownloadStatus_ReportsRealFailures(t *testing.T) {
	client := albumClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if _, err := client.GetDownloadStatus([]*models.Track{{File: "song.flac"}}); err == nil {
		t.Error("GetDownloadStatus() = nil, want an error when slskd fails")
	}
}
