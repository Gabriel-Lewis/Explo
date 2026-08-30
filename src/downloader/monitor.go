package downloader

import (
	"explo/src/logging"
	"explo/src/models"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// maxPollFailures is how many consecutive status polls may fail before the
// monitor gives up. Anything less than the whole run is worth surviving: the
// downloads themselves keep going, and only the monitor loses sight of them.
const maxPollFailures = 3

type Monitor interface {
	GetDownloadStatus([]*models.Track) (map[string]FileStatus, error)
	GetConf() (MonitorConfig, error)
	Cleanup(models.Track, string) error
}

type MonitorConfig struct {
	CheckInterval   time.Duration
	MonitorDuration time.Duration
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

func (c *DownloadClient) MonitorDownloads(tracks []*models.Track, m Monitor) error {
	var successDownloads int

	progressMap := make(map[string]*DownloadMonitor)
	monCfg, err := m.GetConf()
	if err != nil {
		return err
	}

	ticker := time.NewTicker(monCfg.CheckInterval)
	defer ticker.Stop()

	var pollFailures int

	for range ticker.C {
		statuses, err := m.GetDownloadStatus(tracks)
		if err != nil {
			// A failed poll used to end monitoring for every track at once, so
			// one blip -- a restarted service, a dropped connection -- cost the
			// whole playlist, since tracks that never reach Present are dropped
			// before it is built. Ride out a few and try again on the next tick.
			pollFailures++
			if pollFailures >= maxPollFailures {
				return fmt.Errorf("[%s/monitor] error fetching download status %d times in a row, giving up: %s",
					monCfg.Service, pollFailures, err.Error())
			}
			slog.Warn("[monitor] couldn't fetch download status, retrying",
				"service", monCfg.Service, "attempt", pollFailures, "context", err.Error())
			continue
		}
		pollFailures = 0

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
					if err = c.MoveDownload(monCfg.FromDir, monCfg.ToDir, path, track); err != nil {
						slog.Error("error while moving file", "err", err.Error())
					} else {
						slog.Info("track moved successfully", "service", monCfg.Service)
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
			// Exit condition: all tracks have been processed or skipped
		if tracksProcessed(tracks, progressMap) {
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