package downloader

import (
	"explo/src/logging"
	"explo/src/models"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
)

type Monitor interface {
	GetDownloadStatus([]*models.Track) (map[string]FileStatus, error)
	GetConf() (MonitorConfig, error)
	Cleanup(models.Track, string) error
}

type MonitorConfig struct {
	CheckInterval   time.Duration
	MonitorDuration time.Duration
	// MaxRuntime caps the whole monitoring pass. Zero means no cap.
	MaxRuntime      time.Duration
	MigrateDownload bool
	FromDir         string
	ToDir           string
	Service			string
}

type FileStatus struct {
	ID               string    `json:"id"`
	Filename         string    `json:"filename"`
	Size             int       `json:"size"`
	State            string    `json:"state"`
	BytesTransferred int       `json:"bytesTransferred"`
	BytesRemaining   int       `json:"bytesRemaining"`
	PercentComplete  float64   `json:"percentComplete"`
}

// pendingAlbum is a release whose recommended track has already been migrated
// but whose remaining files are still downloading. The monitor revisits these
// each tick, because the recommended track is queued first and so normally
// finishes well ahead of its siblings.
type pendingAlbum struct {
	track    *models.Track
	trackDir string
	destDir  string
	// remaining is the count the migrator last reported. Progress is measured
	// against this rather than against the migrator's own bookkeeping, so the
	// deadline depends only on what the interface returns.
	remaining  int
	lastChange time.Time
}

func (c *DownloadClient) MonitorDownloads(tracks []*models.Track, m Monitor) error {
	var successDownloads int

	progressMap := make(map[string]*DownloadMonitor)
	pendingAlbums := make(map[string]*pendingAlbum)
	monCfg, err := m.GetConf()
	if err != nil {
		return err
	}

	ticker := time.NewTicker(monCfg.CheckInterval)
	defer ticker.Stop()

	var deadline time.Time
	if monCfg.MaxRuntime > 0 {
		deadline = time.Now().Add(monCfg.MaxRuntime)
	}

	for range ticker.C {
		// MonitorDuration only gives up on a download that has stopped moving.
		// A transfer trickling in a few bytes per tick resets that clock every
		// time, so without a cap on the pass as a whole one slow peer holds the
		// entire playlist. Stop and build the playlist from whatever finished.
		if !deadline.IsZero() && time.Now().After(deadline) {
			slog.Warn("[monitor] hit the monitoring time limit, continuing without the rest",
				"service", monCfg.Service, "limit", monCfg.MaxRuntime,
				"downloaded files", successDownloads, "total tracks", len(tracks))
			return nil
		}

		statuses, err := m.GetDownloadStatus(tracks)
		if err != nil {
			return fmt.Errorf("[%s/monitor] error fetching download status: %s", monCfg.Service, err.Error())
		}

		currentTime := time.Now().Local()

		for _, track := range tracks {

			key := fmt.Sprintf("%s|%s", track.ID, track.File)

			if track.Present || track.ID == "" || (progressMap[key] != nil && progressMap[key].Skipped) {
				continue
			}

			// Initialize tracker if not present
			if _, exists := progressMap[key]; !exists {
				progressMap[key] = &DownloadMonitor{
					LastBytesTransferred: 0,
					Counter:              0,
					LastUpdated:          currentTime,
				}
			}
			fileStatus, exists := statuses[track.File]
			tracker := progressMap[key]
			if !exists {
				tracker.Counter++

				if tracker.Counter >= 2 {
					slog.Info("[monitor] track not found in queue after retries, skipping", "service", monCfg.Service,"track title", track.CleanTitle, "track artist", track.MainArtist)
					tracker.Skipped = true
				}
				continue
			}
			monitoredTime := currentTime.Sub(tracker.LastUpdated)

			if fileStatus.BytesRemaining == 0 || fileStatus.PercentComplete == 100 || strings.Contains(fileStatus.State, "Succeeded") {		
				track.Present = true
				slog.Info("[monitor] file downloaded successfully", "service", monCfg.Service, "file", track.File)
				var path string
				track.File, path = parsePath(track.File)
				if monCfg.MigrateDownload {
					destDir, err := c.MoveDownload(monCfg.FromDir, monCfg.ToDir, path, track)
					if err != nil {
						slog.Error("error while moving file", "err", err.Error())
					} else {
						slog.Info("track moved successfully", "service", monCfg.Service)
						c.trackAlbum(pendingAlbums, key, &pendingAlbum{
							track:      track,
							trackDir:   filepath.Join(monCfg.FromDir, path),
							destDir:    destDir,
							lastChange: currentTime,
						}, monCfg)
					}
				}
				delete(progressMap, key)
				successDownloads += 1
				if err = m.Cleanup(*track, fileStatus.ID); err != nil {
					slog.Debug("cleanup failed", logging.RuntimeAttr(err.Error()))
				}
				continue

			} else if fileStatus.BytesTransferred > tracker.LastBytesTransferred {
				tracker.LastBytesTransferred = fileStatus.BytesTransferred
				tracker.LastUpdated = currentTime
				slog.Info("[monitor] progress updated", "service", monCfg.Service, "file", track.File, "bytes transferred", fileStatus.BytesTransferred)
				continue

			} else if monitoredTime > monCfg.MonitorDuration || fileStatus.State == "Errored" {
				slog.Info("[monitor] no download progress for file, skipping", "service", monCfg.Service, "file", track.File, "state", fileStatus.State, "duration", monitoredTime,)
				tracker.Skipped = true
				if err = m.Cleanup(*track, fileStatus.ID); err != nil {
					slog.Debug("cleanup failed", logging.RuntimeAttr(err.Error()))
				}
				continue
			}
		}
		c.migratePendingAlbums(pendingAlbums, monCfg, time.Now().Local())

			// Exit condition: all tracks have been processed or skipped, and
			// every release has finished being migrated
		if tracksProcessed(tracks, progressMap) && len(pendingAlbums) == 0 {
			slog.Info("[monitor] Finished", "service", monCfg.Service, "downloaded files", successDownloads, "total tracks", len(tracks))
			return nil
		}
	}
	return nil
}

// Checks if all tracks are processed (either downloaded or skipped)
func tracksProcessed(tracks []*models.Track, progressMap map[string]*DownloadMonitor) bool {
	for _, track := range tracks {
		key := fmt.Sprintf("%s|%s", track.ID, track.File)
		tracker, exists := progressMap[key]
		if !track.Present && exists && !tracker.Skipped {
			slog.Info("[monitor] track download still in progress", "title", track.CleanTitle, "artist", track.MainArtist, "file", track.File)
			return false
		}
	}
	return true
}
// trackAlbum records a release for later migration, migrating whatever has
// already finished. Releases with nothing left over are never recorded, so a
// single-track download costs nothing.
func (c *DownloadClient) trackAlbum(pending map[string]*pendingAlbum, key string, album *pendingAlbum, monCfg MonitorConfig) {
	if remaining := c.migrateAlbum(album); remaining > 0 {
		album.remaining = remaining
		pending[key] = album
		slog.Info("[monitor] waiting for the rest of the release", "service", monCfg.Service,
			"album", album.track.Album, "files", remaining)
	}
}

// migrateAlbum moves whatever of one release has finished, returning how many
// of its files are still in flight.
func (c *DownloadClient) migrateAlbum(album *pendingAlbum) int {
	var remaining int
	for _, m := range c.albumMigrators() {
		remaining += m.MoveAlbumSiblings(album.trackDir, album.destDir, album.track, c.Cfg.KeepPermissions)
	}
	return remaining
}

// migratePendingAlbums revisits every release still waiting on files. A release
// that stops making progress is abandoned on the same deadline a stalled track
// is, so the monitor cannot be held open by a peer that has gone away.
func (c *DownloadClient) migratePendingAlbums(pending map[string]*pendingAlbum, monCfg MonitorConfig, now time.Time) {
	for key, album := range pending {
		before := album.remaining
		remaining := c.migrateAlbum(album)
		album.remaining = remaining

		if remaining == 0 {
			delete(pending, key)
			if err := removeDirIfEmpty(album.trackDir); err != nil {
				slog.Debug("couldn't clean up release directory", "context", err.Error())
			}
			slog.Info("[monitor] release fully migrated", "service", monCfg.Service, "album", album.track.Album)
			continue
		}

		if remaining < before {
			album.lastChange = now
			continue
		}

		if now.Sub(album.lastChange) > monCfg.MonitorDuration {
			delete(pending, key)
			slog.Warn("[monitor] giving up on the rest of the release", "service", monCfg.Service,
				"album", album.track.Album, "files left in place", remaining)
		}
	}
}
