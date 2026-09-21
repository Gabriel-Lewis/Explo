package client

import (
	"testing"

	"explo/src/models"
	"explo/src/util"
)

func plexTrackMetadata() []SongMetadata {
	return []SongMetadata{
		{Type: "track", RatingKey: "1", Key: "/library/metadata/1", Title: "hurt",
			ParentTitle: "The Downward Spiral", GrandparentTitle: "Nine Inch Nails"},
		{Type: "track", RatingKey: "2", Key: "/library/metadata/2", Title: "hurt",
			ParentTitle: "American IV", GrandparentTitle: "Johnny Cash"},
	}
}

// getPlexMBID costs a round-trip per result. When the track carries no
// MusicBrainz ID there is nothing to compare the answer against, so the whole
// lookup is wasted -- and it is wasted once per result, twice per run.
func TestPlexGetSong_SkipsTheMBIDLookupWhenTheTrackHasNoMBID(t *testing.T) {
	var paths []string
	server := recordingServer(t, &paths)

	plex := NewPlex(testConfig(server.URL, ""), util.NewHttp(util.HttpClientConfig{Timeout: 5}))
	track := &models.Track{CleanTitle: "hurt", MainArtist: "Nine Inch Nails", Album: "The Downward Spiral"}

	if _, err := plex.getPlexSong(track, plexTrackMetadata()); err != nil {
		t.Fatalf("getPlexSong() = %v, want a match", err)
	}

	if len(paths) != 0 {
		t.Errorf("made %d metadata requests (%v), want none", len(paths), paths)
	}
}

// When the track does carry an MBID the lookup is still made, because the
// answer can decide the match outright.
func TestPlexGetSong_LooksUpTheMBIDWhenTheTrackHasOne(t *testing.T) {
	var paths []string
	server := recordingServer(t, &paths)

	plex := NewPlex(testConfig(server.URL, ""), util.NewHttp(util.HttpClientConfig{Timeout: 5}))
	track := &models.Track{
		CleanTitle:         "hurt",
		MainArtist:         "Nine Inch Nails",
		Album:              "The Downward Spiral",
		MusicBrainzTrackID: "mbid-1",
	}

	if _, err := plex.getPlexSong(track, plexTrackMetadata()); err != nil {
		t.Fatalf("getPlexSong() = %v, want a match", err)
	}

	if len(paths) == 0 {
		t.Error("made no metadata request, want one per result")
	}
}
