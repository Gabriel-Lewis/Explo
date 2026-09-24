package downloader

import (
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"sync"

	"explo/src/models"
	"explo/src/util"
)

// In album mode every recommended track used to fetch its own copy of its
// album. A playlist carrying three songs from one record therefore downloaded
// that record three times, usually from three different peers, and the copies
// all landed in the library. releaseRegistry makes the first track of an album
// the one that downloads it; the others wait for that release and take their
// own file out of it.

// trackRole is how a track is being fetched within a run.
type trackRole int

const (
	// roleAlbum is the default: the track searches for and queues its own release.
	roleAlbum trackRole = iota
	// roleLeader is roleAlbum for a track whose release others are waiting on.
	roleLeader
	// roleShared tracks were found in a release another track queued, so
	// there is nothing left to download for them.
	roleShared
	// roleSingle tracks share an album with a queued release that lacks them,
	// so they fall back to a single-track download.
	roleSingle
)

type trackState struct {
	role    trackRole
	key     string
	release *sharedRelease
}

// sharedRelease is one release queued on behalf of every track from its album.
type sharedRelease struct {
	done   chan struct{}
	leader *models.Track
	// files is what the leader queued, primary first. It stays nil if the
	// leader failed, which tells waiting tracks to try the album themselves.
	files []File
	// claimed holds the files already spoken for, so two tracks can never be
	// handed the same file.
	claimed map[string]bool
}

type releaseRegistry struct {
	mu       sync.Mutex
	releases map[string]*sharedRelease
	tracks   map[*models.Track]*trackState
	// sharedIDs are the IDs handed to shared tracks. They name no slskd
	// search, so Cleanup must not try to delete one.
	sharedIDs map[string]bool
}

func newReleaseRegistry() *releaseRegistry {
	return &releaseRegistry{
		releases:  make(map[string]*sharedRelease),
		tracks:    make(map[*models.Track]*trackState),
		sharedIDs: make(map[string]bool),
	}
}

// albumKey identifies the album a track belongs to. The release group is
// preferred because it is the same for every edition of an album, which the
// album name is not. Tracks with no album cannot share a release.
func albumKey(track models.Track) string {
	if track.MusicBrainzReleaseGroupID != "" {
		return "rg:" + track.MusicBrainzReleaseGroupID
	}
	artist := strings.ToLower(util.AlnumOnly(track.MainArtist))
	album := strings.ToLower(util.AlnumOnly(track.Album))
	if artist == "" || album == "" {
		return ""
	}
	return "name:" + artist + "\x00" + album
}

// claim returns the release for key, creating it if this is the first track
// to ask. The creator is the leader and must resolve it with publish or fail.
func (r *releaseRegistry) claim(key string, track *models.Track) (*sharedRelease, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if rel, ok := r.releases[key]; ok {
		return rel, false
	}

	rel := &sharedRelease{
		done:    make(chan struct{}),
		leader:  track,
		claimed: make(map[string]bool),
	}
	r.releases[key] = rel
	r.tracks[track] = &trackState{role: roleLeader, key: key, release: rel}
	return rel, true
}

// publish hands the queued release to every track waiting on it.
func (r *releaseRegistry) publish(state *trackState, files []File) {
	r.mu.Lock()
	defer r.mu.Unlock()

	state.release.files = files
	state.release.claimed[files[0].Name] = true
	close(state.release.done)
}

// fail releases the claim so that a waiting track can lead the album instead.
// Failing is specific to the leader -- its track may be missing from every
// release found -- so another track from the album may well succeed.
func (r *releaseRegistry) fail(state *trackState) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.releases[state.key] == state.release {
		delete(r.releases, state.key)
	}
	state.role = roleAlbum
	close(state.release.done)
}

func (r *releaseRegistry) state(track *models.Track) *trackState {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tracks[track]
}

func (r *releaseRegistry) setRole(track *models.Track, role trackRole) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tracks[track] = &trackState{role: role}
}

func (r *releaseRegistry) isSharedID(id string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sharedIDs[id]
}

// share looks for track inside a published release. If it is there, the track
// follows that file instead of downloading anything: it is taken out of the
// leader's siblings so the monitor migrates it as a track in its own right,
// with its own metadata and path.
func (r *releaseRegistry) share(rel *sharedRelease, track *models.Track) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	candidates := peerDir{username: rel.files[0].Username}
	for _, file := range rel.files {
		if !rel.claimed[file.Name] {
			candidates.files = append(candidates.files, file)
		}
	}
	if !findPrimary(&candidates, *track) {
		return false
	}
	file := *candidates.primary
	rel.claimed[file.Name] = true

	rel.leader.AlbumFiles = slices.DeleteFunc(rel.leader.AlbumFiles, func(name string) bool {
		return name == string(file.Name)
	})

	// The monitor looks statuses up by ID, so each shared track needs one of
	// its own even though it has no search behind it.
	track.ID = fmt.Sprintf("%s:shared:%d", rel.leader.ID, len(rel.claimed))
	r.sharedIDs[track.ID] = true
	r.tracks[track] = &trackState{role: roleShared}

	track.MainArtistID = file.Username
	track.Size = file.Size
	track.File = string(file.Name)

	slog.Info("track found in a release already queued, not downloading it again",
		"track", track.CleanTitle, "album", track.Album, "file", path.Base(normalizePeerPath(track.File)))
	return true
}

// queryAlbum is QueryTrack in album mode. The first track of an album searches
// for it; the rest wait to see what it queued.
func (c *Slskd) queryAlbum(track *models.Track) error {
	key := albumKey(*track)
	if c.releases == nil || key == "" {
		return c.search(track, true)
	}

	for {
		rel, leader := c.releases.claim(key, track)
		if leader {
			if err := c.search(track, true); err != nil {
				c.releases.fail(c.releases.state(track))
				return err
			}
			return nil
		}

		<-rel.done
		if rel.files == nil {
			// The leader found nothing. Try to lead the album ourselves.
			continue
		}
		if c.releases.share(rel, track) {
			return nil
		}

		slog.Info("track missing from the release queued for its album, downloading it alone",
			"track", track.CleanTitle, "album", track.Album)
		c.releases.setRole(track, roleSingle)
		return c.search(track, false)
	}
}

// getAlbum is GetTrack in album mode. A leader resolves its release whatever
// happens, since other tracks are blocked until it does.
func (c *Slskd) getAlbum(track *models.Track) error {
	state := c.releases.state(track)

	files, err := c.queueAlbum(track)
	if state != nil && state.role == roleLeader {
		if err != nil {
			c.releases.fail(state)
		} else {
			c.releases.publish(state, files)
		}
	}
	return err
}

func (c *Slskd) queueAlbum(track *models.Track) ([]File, error) {
	results, err := c.searchResults(track.ID)
	if err != nil {
		return nil, err
	}
	files, err := c.CollectAlbumFiles(*track, results)
	if err != nil {
		return nil, err
	}
	if err := c.queueAlbumDownload(files, track); err != nil {
		return nil, err
	}
	return files, nil
}
