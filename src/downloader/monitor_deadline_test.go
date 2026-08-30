package downloader

import (
	"testing"
	"time"

	cfg "explo/src/config"
	"explo/src/models"
)

// crawlingStub always reports a download inching forward, which is exactly the
// case MonitorDuration cannot catch: every tick moves a byte, so the stall
// clock resets forever.
type crawlingStub struct {
	conf  MonitorConfig
	polls int
}

func (c *crawlingStub) QueryTrack(*models.Track) error     { return nil }
func (c *crawlingStub) GetTrack(*models.Track) error       { return nil }
func (c *crawlingStub) GetConf() (MonitorConfig, error)    { return c.conf, nil }
func (c *crawlingStub) Cleanup(models.Track, string) error { return nil }

func (c *crawlingStub) GetDownloadStatus(tracks []*models.Track) (map[string]FileStatus, error) {
	c.polls++

	statuses := make(map[string]FileStatus, len(tracks))
	for _, track := range tracks {
		statuses[track.File] = FileStatus{
			ID:               "1",
			State:            "InProgress",
			BytesTransferred: c.polls, // always moving, never done
			BytesRemaining:   1_000_000,
		}
	}
	return statuses, nil
}

// completingStub finishes the download after a set number of polls.
type completingStub struct {
	conf  MonitorConfig
	after int // polls of progress before it completes
	polls int
}

func (c *completingStub) QueryTrack(*models.Track) error     { return nil }
func (c *completingStub) GetTrack(*models.Track) error       { return nil }
func (c *completingStub) GetConf() (MonitorConfig, error)    { return c.conf, nil }
func (c *completingStub) Cleanup(models.Track, string) error { return nil }

func (c *completingStub) GetDownloadStatus(tracks []*models.Track) (map[string]FileStatus, error) {
	c.polls++

	statuses := make(map[string]FileStatus, len(tracks))
	for _, track := range tracks {
		if c.polls > c.after {
			statuses[track.File] = FileStatus{ID: "1", State: "Completed, Succeeded", PercentComplete: 100}
			continue
		}
		statuses[track.File] = FileStatus{ID: "1", State: "InProgress", BytesTransferred: c.polls, BytesRemaining: 100}
	}
	return statuses, nil
}

func runUntilReturn(t *testing.T, stub Monitor, track *models.Track) error {
	t.Helper()

	client := &DownloadClient{Cfg: &cfg.DownloadConfig{}}

	done := make(chan error, 1)
	go func() { done <- client.MonitorDownloads([]*models.Track{track}, stub) }()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("MonitorDownloads never returned; the time limit did not apply")
		return nil
	}
}

// A download that keeps inching forward resets the stall clock on every tick,
// so before the cap existed one slow peer held the playlist open forever.
func TestMonitorDownloads_StopsAtTheTimeLimit(t *testing.T) {
	stub := &crawlingStub{conf: MonitorConfig{
		CheckInterval:   time.Millisecond,
		MonitorDuration: time.Hour, // never trips: the transfer is progressing
		MaxRuntime:      50 * time.Millisecond,
		Service:         "slskd",
	}}

	track := &models.Track{ID: "track-1", File: "song.flac"}

	if err := runUntilReturn(t, stub, track); err != nil {
		t.Fatalf("MonitorDownloads() = %v, want nil so the playlist is still built", err)
	}
	if track.Present {
		t.Error("an unfinished track was marked present")
	}
}

// The cap must not cut a run short when everything finishes normally.
func TestMonitorDownloads_TimeLimitDoesNotDisturbANormalRun(t *testing.T) {
	stub := &completingStub{conf: MonitorConfig{
		CheckInterval:   time.Millisecond,
		MonitorDuration: time.Hour,
		MaxRuntime:      time.Hour,
		Service:         "slskd",
	}}

	track := &models.Track{ID: "track-1", File: "song.flac"}

	if err := runUntilReturn(t, stub, track); err != nil {
		t.Fatalf("MonitorDownloads() = %v, want nil", err)
	}
	if !track.Present {
		t.Error("a completed track was not marked present")
	}
}

// Zero means no cap, for anyone who would rather wait indefinitely.
func TestMonitorDownloads_ZeroDisablesTheTimeLimit(t *testing.T) {
	// Several ticks of progress before completing, well past any accidental
	// treatment of a zero deadline as "already expired".
	stub := &completingStub{after: 3, conf: MonitorConfig{
		CheckInterval:   time.Millisecond,
		MonitorDuration: time.Hour,
		MaxRuntime:      0,
		Service:         "slskd",
	}}

	track := &models.Track{ID: "track-1", File: "song.flac"}

	if err := runUntilReturn(t, stub, track); err != nil {
		t.Fatalf("MonitorDownloads() = %v, want nil", err)
	}
	if !track.Present {
		t.Error("a zero limit stopped the run instead of disabling the cap")
	}
}
