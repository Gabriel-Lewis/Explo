package downloader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"explo/src/models"
	"explo/src/util"
)

// Soulseek peers report Windows-style paths. Normalising once means the rest of
// this file can treat them as ordinary slash-separated paths.
func normalizePeerPath(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}

// peerDir is one candidate release: the files a single peer shares from a
// single directory.
type peerDir struct {
	username string
	dir      string
	files    []File
	// primary is the file matching the recommended track. A directory without
	// one is not a candidate, since the recommendation is what we came for.
	primary *File
}

// groupByDirectory buckets search results by peer and parent directory, keeping
// only files whose extension is allowed. Quality filtering happens later, per
// candidate, so that one bad file does not disqualify a whole release.
func (c Slskd) groupByDirectory(results SearchResults) []peerDir {
	index := make(map[string]*peerDir)
	var order []string

	for _, result := range results {
		if result.FileCount == 0 || !result.HasFreeUploadSlot {
			continue
		}
		for _, file := range result.Files {
			file.Extension = resolveExtension(file)
			if !slices.Contains(c.Cfg.Filters.Extensions, file.Extension) {
				continue
			}

			dir := path.Dir(normalizePeerPath(string(file.Name)))
			key := result.Username + "\x00" + dir

			if _, ok := index[key]; !ok {
				index[key] = &peerDir{username: result.Username, dir: dir}
				order = append(order, key)
			}
			file.Username = result.Username
			index[key].files = append(index[key].files, file)
		}
	}

	dirs := make([]peerDir, 0, len(order))
	for _, key := range order {
		dirs = append(dirs, *index[key])
	}
	return dirs
}

// resolveExtension prefers the extension in the filename over the one the peer
// reports, matching what CollectFiles does for single tracks.
func resolveExtension(file File) string {
	nameExt := util.AlnumOnly(strings.TrimPrefix(strings.ToLower(path.Ext(normalizePeerPath(string(file.Name)))), "."))
	if nameExt != "" {
		return nameExt
	}
	return strings.TrimPrefix(strings.ToLower(file.Extension), ".")
}

// findPrimary locates the recommended track inside a candidate directory.
// Without it the directory may well be the right album, but we would be
// downloading it without the track that prompted the recommendation.
func findPrimary(dir *peerDir, track models.Track) bool {
	sanitizedTitle := util.AlnumOnly(track.CleanTitle)
	if sanitizedTitle == "" {
		return false
	}

	for i := range dir.files {
		name := util.AlnumOnly(path.Base(normalizePeerPath(string(dir.files[i].Name))))
		if !containsLower(name, sanitizedTitle) {
			continue
		}
		// Duration is only meaningful for the recommended track; sibling tracks
		// legitimately differ, which is why this check is not applied to them.
		if track.Duration > 0 && dir.files[i].Length > 0 &&
			util.Abs(track.Duration/1000-dir.files[i].Length) > 10 {
			continue
		}
		dir.primary = &dir.files[i]
		return true
	}
	return false
}

// scoreDir ranks a candidate release. A directory naming the album beats one
// that merely happens to contain the track, and among equals the fuller
// release wins.
func scoreDir(dir peerDir, track models.Track) int {
	var score int

	sanitizedDir := util.AlnumOnly(dir.dir)
	if album := util.AlnumOnly(track.Album); album != "" && containsLower(sanitizedDir, album) {
		score += 100
	}
	if artist := util.AlnumOnly(track.MainArtist); artist != "" && containsLower(sanitizedDir, artist) {
		score += 50
	}

	// More tracks means a more complete release, but never let file count
	// outweigh actually matching the album.
	score += min(len(dir.files), 40)

	return score
}

// qualityFiltered drops files failing the bitrate and bit-depth floors. Unlike
// filterFiles it does not cap the count -- the whole release is the point --
// and it leaves the primary in place even if it fails, since a playlist entry
// with a mediocre file beats no entry at all.
func (c Slskd) qualityFiltered(dir peerDir) []File {
	kept := make([]File, 0, len(dir.files))

	for _, file := range dir.files {
		isPrimary := dir.primary != nil && file.Name == dir.primary.Name
		if !isPrimary {
			if file.BitRate > 0 && file.BitRate < c.Cfg.Filters.MinBitRate {
				continue
			}
			if file.BitDepth > 0 && file.BitDepth < c.Cfg.Filters.MinBitDepth {
				continue
			}
		}
		kept = append(kept, file)
	}
	return kept
}

// CollectAlbumFiles picks the release to download. It returns the files to
// queue with the recommended track first, so callers can treat files[0] as the
// one that belongs in the playlist.
func (c Slskd) CollectAlbumFiles(track models.Track, results SearchResults) ([]File, error) {
	candidates := c.groupByDirectory(results)

	var best *peerDir
	var bestScore int

	for i := range candidates {
		dir := &candidates[i]

		// A keyword hit on the directory rejects the whole release -- this is
		// how "live" or "remix" pressings get excluded in album mode.
		if ContainsKeyword(track, dir.dir, c.Cfg.Filters.FilterList) {
			continue
		}
		if !findPrimary(dir, track) {
			continue
		}
		if score := scoreDir(*dir, track); best == nil || score > bestScore {
			best, bestScore = dir, score
		}
	}

	if best == nil {
		return nil, fmt.Errorf("no release found containing %s - %s", track.MainArtist, track.CleanTitle)
	}

	files := c.qualityFiltered(*best)
	if len(files) == 0 {
		return nil, fmt.Errorf("no files passed filters in %s", best.dir)
	}

	// Primary first: queueAlbumDownload and the monitor both rely on it.
	primaryName := best.primary.Name
	slices.SortStableFunc(files, func(a, b File) int {
		switch {
		case a.Name == primaryName:
			return -1
		case b.Name == primaryName:
			return 1
		default:
			return 0
		}
	})

	slog.Info("album release selected",
		"dir", best.dir, "user", best.username, "tracks", len(files), "score", bestScore)

	return files, nil
}

// queueAlbumDownload queues an entire release in a single request. slskd takes
// an array of files per peer, so the whole directory goes in one call.
func (c Slskd) queueAlbumDownload(files []File, track *models.Track) error {
	if len(files) == 0 {
		return fmt.Errorf("no files to queue for %s - %s", track.CleanTitle, track.Artist)
	}

	primary := files[0]

	payload := make([]DownloadPayload, 0, len(files))
	for _, file := range files {
		payload = append(payload, DownloadPayload{Filename: string(file.Name), Size: file.Size})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %s", err.Error())
	}

	reqParams := fmt.Sprintf("/api/v0/transfers/downloads/%s", primary.Username)
	if _, err := c.HttpClient.MakeRequest("POST", c.Cfg.URL+reqParams, bytes.NewBuffer(body), c.Headers); err != nil {
		return fmt.Errorf("couldn't queue release for %s - %s: %s", track.CleanTitle, track.Artist, err.Error())
	}

	// The monitor and the playlist follow the primary; the rest ride along.
	track.MainArtistID = primary.Username
	track.Size = primary.Size
	track.File = string(primary.Name)

	track.AlbumFiles = track.AlbumFiles[:0]
	for _, file := range files[1:] {
		track.AlbumFiles = append(track.AlbumFiles, string(file.Name))
	}

	slog.Info("queued release",
		"track", track.CleanTitle, "artist", track.MainArtist, "files", len(files))

	return nil
}

// albumSearchTerm is what we ask slskd for in album mode. Falling back to the
// track keeps releases with no album metadata working.
func albumSearchTerm(track models.Track) string {
	if strings.TrimSpace(track.Album) == "" {
		return fmt.Sprintf("%s - %s", track.CleanTitle, track.Artist)
	}
	return fmt.Sprintf("%s - %s", track.MainArtist, track.Album)
}

// siblingStates reports the transfer state slskd has for each file it is
// downloading from a peer, keyed by the peer's path for that file.
func (c Slskd) siblingStates(username string) (map[string]string, error) {
	body, err := c.HttpClient.MakeRequest("GET", c.Cfg.URL+"/api/v0/transfers/downloads", nil, c.Headers)
	if err != nil {
		return nil, err
	}

	var statuses DownloadStatus
	if err := util.ParseResp(body, &statuses); err != nil {
		return nil, err
	}

	states := make(map[string]string)
	for _, status := range statuses {
		if status.Username != username {
			continue
		}
		for _, dir := range status.Directories {
			for _, file := range dir.Files {
				states[string(file.Name)] = normalize(file.State)
			}
		}
	}
	return states, nil
}

// MoveAlbumSiblings migrates whatever of the release has finished downloading
// and reports how many files are still in flight, so callers can come back for
// the rest. Doing this once was not enough: the recommended track is queued
// first and so usually finishes first, which left most of the album skipped and
// never looked at again.
//
// A sibling still transferring is left where it is -- copying a partial file
// would put a truncated track in the library. Migrated files are dropped from
// track.AlbumFiles, and so are ones slskd has given up on, which makes repeated
// calls idempotent and lets the count reach zero.
//
// keepPermissions is passed in rather than read from the slskd config because
// it is a download-wide setting owned by DownloadClient, which is also what
// calls this.
func (c Slskd) MoveAlbumSiblings(trackDir, destDir string, track *models.Track, keepPermissions bool) int {
	if !c.Cfg.AlbumMode || len(track.AlbumFiles) == 0 {
		return 0
	}

	states, err := c.siblingStates(track.MainArtistID)
	if err != nil {
		// Nothing is dropped on a failed lookup: the files are still there and
		// the next attempt can still find them.
		slog.Warn("couldn't check release download states, will retry",
			"album", track.Album, "context", err.Error())
		return len(track.AlbumFiles)
	}

	var moved, failed int
	pending := make([]string, 0, len(track.AlbumFiles))

	for _, peerPath := range track.AlbumFiles {
		state := states[peerPath]
		name := path.Base(normalizePeerPath(peerPath))

		switch {
		case strings.Contains(state, "Succeeded"):
			if err := copyFile(filepath.Join(trackDir, name), filepath.Join(destDir, name), keepPermissions); err != nil {
				// Keep it pending: slskd can report a transfer complete a moment
				// before the file is readable.
				slog.Debug("album track not ready to move yet", "file", name, "context", err.Error())
				pending = append(pending, peerPath)
				continue
			}
			moved++

		case state == errorState:
			// slskd has given up on this one, so waiting for it would only hold
			// the release open until the deadline.
			slog.Debug("album track failed to download, giving up on it", "file", name)
			failed++

		default:
			pending = append(pending, peerPath)
		}
	}

	track.AlbumFiles = pending

	if moved > 0 || failed > 0 {
		slog.Info("migrated release", "album", track.Album,
			"moved", moved, "failed", failed, "still downloading", len(pending))
	}

	return len(pending)
}

// copyFile moves one finished album track into the library, mirroring how
// MoveDownload handles the recommended track: copy, sync, optionally preserve
// permissions, then drop the original.
func copyFile(src, dst string, keepPermissions bool) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("couldn't open source file: %s", err.Error())
	}
	defer func() {
		if cerr := in.Close(); cerr != nil {
			slog.Error(fmt.Sprintf("failed to close source file: %s", cerr.Error()))
		}
	}()

	if err = os.MkdirAll(filepath.Dir(dst), os.ModePerm); err != nil {
		return fmt.Errorf("couldn't make destination directory: %s", err.Error())
	}

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("couldn't create destination file: %s", err.Error())
	}
	defer func() {
		if cerr := out.Close(); cerr != nil {
			slog.Error(fmt.Sprintf("failed to close destination file: %s", cerr.Error()))
		}
	}()

	if _, err = io.Copy(out, in); err != nil {
		return fmt.Errorf("copy failed: %s", err.Error())
	}
	if err = out.Sync(); err != nil {
		return fmt.Errorf("sync failed: %s", err.Error())
	}

	if keepPermissions {
		info, err := os.Stat(src)
		if err != nil {
			return fmt.Errorf("stat error: %s", err.Error())
		}
		if err = os.Chmod(dst, info.Mode()); err != nil {
			return fmt.Errorf("chmod failed: %s", err.Error())
		}
	}

	return os.Remove(src)
}
