package downloader

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"explo/src/models"

	"golang.org/x/sync/errgroup"
)

// fakeSlskd answers searches from a fixed catalogue and records what was
// searched for and queued.
type fakeSlskd struct {
	t       *testing.T
	catalog func(query string) SearchResults

	mu       sync.Mutex
	searches []string
	byID     map[string]string
	queued   [][]DownloadPayload
	deleted  []string
}

func (f *fakeSlskd) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var resp any = struct{}{}
	p := r.URL.Path

	switch {
	case r.Method == http.MethodPost && p == "/api/v0/searches":
		var body SearchPayload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("decoding search: %v", err)
		}
		f.searches = append(f.searches, body.SearchText)
		id := fmt.Sprintf("search-%d", len(f.searches))
		f.byID[id] = body.SearchText
		resp = Search{ID: id}

	case r.Method == http.MethodGet && strings.HasSuffix(p, "/responses"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/api/v0/searches/"), "/responses")
		resp = f.catalog(f.byID[id])

	case r.Method == http.MethodGet && strings.HasPrefix(p, "/api/v0/searches/"):
		id := strings.TrimPrefix(p, "/api/v0/searches/")
		count := 0
		for _, result := range f.catalog(f.byID[id]) {
			count += len(result.Files)
		}
		resp = Search{ID: id, IsComplete: true, FileCount: count}

	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/api/v0/searches/"):
		f.deleted = append(f.deleted, strings.TrimPrefix(p, "/api/v0/searches/"))

	case r.Method == http.MethodPost && strings.HasPrefix(p, "/api/v0/transfers/downloads/"):
		body, _ := io.ReadAll(r.Body)
		var payload []DownloadPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			f.t.Errorf("decoding queue request: %v", err)
		}
		f.queued = append(f.queued, payload)
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		f.t.Errorf("fake slskd failed to write response: %v", err)
	}
}

func newFakeSlskd(t *testing.T, catalog func(string) SearchResults) (*fakeSlskd, *Slskd) {
	t.Helper()
	fake := &fakeSlskd{t: t, catalog: catalog, byID: make(map[string]string)}
	client := albumClient(t, fake.handler)
	return fake, client
}

var rioFiles = []string{
	"01 Rio.flac", "02 My Own Way.flac", "03 Lonely in Your Nightboat.flac",
	"04 Hungry Like the Wolf.flac", "05 Hold Back the Rain.flac", "08 Save a Prayer.flac",
}

// rioCatalog shares Rio from two peers, the way the same album usually turns
// up more than once on Soulseek.
func rioCatalog(query string) SearchResults {
	if strings.Contains(query, "Rio") {
		return albumResults(
			peer{"peer1", `@@a\Duran Duran\Rio`, rioFiles},
			peer{"peer2", `@@b\Music\Duran Duran - Rio (1982)`, rioFiles},
		)
	}
	return nil
}

func rioTrack(title string) *models.Track {
	return &models.Track{
		CleanTitle:                title,
		Title:                     title,
		Artist:                    "Duran Duran",
		MainArtist:                "Duran Duran",
		Album:                     "Rio",
		MusicBrainzReleaseGroupID: "rio-rg",
		Duration:                  200_000,
	}
}

// runTracks drives tracks the way StartDownload does, concurrently.
func runTracks(client *Slskd, tracks ...*models.Track) {
	var g errgroup.Group
	g.SetLimit(3)
	for _, track := range tracks {
		g.Go(func() error {
			if err := client.QueryTrack(track); err != nil {
				return nil
			}
			_ = client.GetTrack(track)
			return nil
		})
	}
	_ = g.Wait()
}

// The reported bug: three songs from Rio in one playlist downloaded Rio three
// times. One release must be queued, and each song must follow its own file
// in it.
func TestAlbumMode_DownloadsEachAlbumOnce(t *testing.T) {
	fake, client := newFakeSlskd(t, rioCatalog)

	tracks := []*models.Track{rioTrack("Rio"), rioTrack("Hungry Like the Wolf"), rioTrack("Save a Prayer")}
	runTracks(client, tracks...)

	if len(fake.searches) != 1 {
		t.Errorf("searched %d times (%q), want once for the album", len(fake.searches), fake.searches)
	}
	if len(fake.queued) != 1 {
		t.Fatalf("queued %d releases, want 1", len(fake.queued))
	}
	if len(fake.queued[0]) != len(rioFiles) {
		t.Errorf("queued %d files, want the whole %d-track release", len(fake.queued[0]), len(rioFiles))
	}

	ids := make(map[string]bool)
	var leader *models.Track
	for _, track := range tracks {
		if track.ID == "" {
			t.Errorf("%s has no ID; the monitor would never look at it", track.CleanTitle)
		}
		if ids[track.ID] {
			t.Errorf("%s shares ID %q with another track; the monitor would confuse them", track.CleanTitle, track.ID)
		}
		ids[track.ID] = true

		if !strings.Contains(track.File, track.CleanTitle) {
			t.Errorf("%s follows %q, want its own file", track.CleanTitle, track.File)
		}
		if len(track.AlbumFiles) > 0 {
			leader = track
		}
	}

	if leader == nil {
		t.Fatal("no track kept the release's siblings")
	}
	for _, track := range tracks {
		if slices.Contains(leader.AlbumFiles, track.File) {
			t.Errorf("%s is still a sibling of the release; it would be migrated twice", track.CleanTitle)
		}
	}
	if want := len(rioFiles) - len(tracks); len(leader.AlbumFiles) != want {
		t.Errorf("release has %d siblings left, want %d", len(leader.AlbumFiles), want)
	}
}

// A song the queued release lacks still has to be downloaded, on its own
// rather than as yet another copy of the album.
func TestAlbumMode_TrackMissingFromReleaseDownloadsAlone(t *testing.T) {
	bonus := "Rio (Night Version)"
	fake, client := newFakeSlskd(t, func(query string) SearchResults {
		if strings.Contains(query, bonus) {
			return albumResults(peer{"peer3", `@@c\Duran Duran\Singles`, []string{"Rio (Night Version).flac"}})
		}
		return rioCatalog(query)
	})
	client.Cfg.DownloadAttempts = 1

	runTracks(client, rioTrack("Hungry Like the Wolf"))
	single := rioTrack(bonus)
	runTracks(client, single)

	if got := fake.searches[len(fake.searches)-1]; got != bonus+" - Duran Duran" {
		t.Errorf("last search = %q, want a single-track search", got)
	}
	if len(fake.queued) != 2 || len(fake.queued[1]) != 1 {
		t.Fatalf("queued %v, want the release then one file", fake.queued)
	}
	if !strings.Contains(single.File, "Night Version") {
		t.Errorf("%s follows %q, want the single file", single.CleanTitle, single.File)
	}
}

// If the first track of an album finds no release with itself in it, the
// album is not written off: the next track tries it.
func TestAlbumMode_AnotherTrackLeadsWhenTheFirstFails(t *testing.T) {
	fake, client := newFakeSlskd(t, rioCatalog)

	unfindable := rioTrack("Not On The Album")
	runTracks(client, unfindable)
	found := rioTrack("Save a Prayer")
	runTracks(client, found)

	if len(fake.queued) != 1 {
		t.Fatalf("queued %d releases, want 1 from the second track", len(fake.queued))
	}
	if !strings.Contains(found.File, "Save a Prayer") {
		t.Errorf("track follows %q, want its own file", found.File)
	}
}

func TestAlbumMode_DifferentAlbumsAreNotShared(t *testing.T) {
	fake, client := newFakeSlskd(t, func(query string) SearchResults {
		if strings.Contains(query, "Seven and the Ragged Tiger") {
			return albumResults(peer{"peer4", `@@d\Duran Duran\Seven and the Ragged Tiger`, []string{"01 The Reflex.flac", "02 New Moon on Monday.flac"}})
		}
		return rioCatalog(query)
	})

	other := rioTrack("The Reflex")
	other.Album, other.MusicBrainzReleaseGroupID = "Seven and the Ragged Tiger", "seven-rg"
	runTracks(client, rioTrack("Rio"), other)

	if len(fake.queued) != 2 {
		t.Errorf("queued %d releases, want one per album", len(fake.queued))
	}
}

// Shared tracks carry an ID that names no search, so cleaning up after one
// must not ask slskd to delete it.
func TestCleanup_SkipsSearchForSharedTracks(t *testing.T) {
	fake, client := newFakeSlskd(t, rioCatalog)

	leader, shared := rioTrack("Rio"), rioTrack("Save a Prayer")
	runTracks(client, leader)
	runTracks(client, shared)

	if !client.releases.isSharedID(shared.ID) {
		t.Fatalf("%s was not shared from the release", shared.CleanTitle)
	}
	fake.deleted = nil
	if err := client.Cleanup(*shared, "file-id"); err != nil {
		t.Fatalf("Cleanup() = %v", err)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("deleted searches %q for a shared track, want none", fake.deleted)
	}
}

func TestAlbumKey(t *testing.T) {
	a, b := rioTrack("Rio"), rioTrack("Rio")
	b.Album = "Rio (2009 Collector's Edition)"
	if albumKey(*a) != albumKey(*b) {
		t.Error("editions of one release group got different keys")
	}

	a.MusicBrainzReleaseGroupID, b.MusicBrainzReleaseGroupID = "", ""
	b.Album = "RIO"
	if albumKey(*a) != albumKey(*b) {
		t.Error("album names differing only in case got different keys")
	}

	a.Album = ""
	if albumKey(*a) != "" {
		t.Error("a track with no album got a key; it would be grouped with others")
	}
}
