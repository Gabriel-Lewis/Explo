package client

import (
	"testing"

	"explo/src/models"
)

// A track that has not been downloaded yet carries no File, and a search
// result without a Media part carries no Path. filepath.Base turns an empty
// string into ".", so comparing the two without checking for emptiness first
// awards the filename bonus to every candidate on the pre-download pass.
func TestBestMatch_EmptyFilenamesEarnNothing(t *testing.T) {
	track := &models.Track{
		CleanTitle: "hurt",
		MainArtist: "Nine Inch Nails",
		Album:      "The Downward Spiral",
	}
	results := []SearchResult{{
		Title:  "hurt me",
		Album:  "American IV",
		Artist: "Johnny Cash",
	}}

	if _, ok := BestMatch(track, results, 60); ok {
		t.Fatal("a different recording matched because both filenames were empty")
	}
}

// The bonus is still worth 50 when the filenames genuinely agree.
func TestBestMatch_EqualFilenamesStillEarnTheBonus(t *testing.T) {
	track := &models.Track{
		CleanTitle: "hurt",
		MainArtist: "Nine Inch Nails",
		Album:      "The Downward Spiral",
		File:       "/downloads/Hurt.flac",
	}
	results := []SearchResult{{
		Title:  "hurt me",
		Album:  "American IV",
		Artist: "Johnny Cash",
		Path:   "/music/hurt.flac",
	}}

	got, ok := BestMatch(track, results, 60)
	if !ok {
		t.Fatal("an equal filename should have carried the result over the threshold")
	}
	if got.Score < 60 {
		t.Fatalf("score = %d, want at least 60", got.Score)
	}
}

// Plex omits OriginalTitle whenever a track's artist equals its album artist,
// which is most of the time, and hub results carry no album at all. Scoring a
// missing field as agreement let an exact title alone clear the threshold.
func TestBestMatch_MissingAlbumAndArtistEarnNothing(t *testing.T) {
	track := &models.Track{
		CleanTitle: "hurt",
		MainArtist: "Nine Inch Nails",
		Album:      "The Downward Spiral",
	}
	results := []SearchResult{{Title: "hurt"}}

	if _, ok := BestMatch(track, results, 60); ok {
		t.Fatal("a same-titled result matched on an absent album and artist")
	}
}

// Two empty albums are not the same album either.
func TestBestMatch_TwoEmptyAlbumsEarnNothing(t *testing.T) {
	track := &models.Track{CleanTitle: "hurt", MainArtist: "Nine Inch Nails"}
	results := []SearchResult{{Title: "hurt", Artist: "Johnny Cash"}}

	if _, ok := BestMatch(track, results, 60); ok {
		t.Fatal("a different artist matched on two empty albums")
	}
}

// Fields that are actually present still score as they did.
func TestBestMatch_PresentAlbumAndArtistStillScore(t *testing.T) {
	track := &models.Track{
		CleanTitle: "hurt",
		MainArtist: "Nine Inch Nails",
		Album:      "The Downward Spiral",
	}
	results := []SearchResult{{
		Title:  "hurt",
		Album:  "The Downward Spiral",
		Artist: "Nine Inch Nails",
	}}

	got, ok := BestMatch(track, results, 60)
	if !ok {
		t.Fatal("an exact title, album and artist should match")
	}
	if got.Score != 95 {
		t.Fatalf("score = %d, want 95 (45 title + 20 album + 30 artist)", got.Score)
	}
}

// A MusicBrainz ID short-circuits scoring entirely.
func TestBestMatch_MBIDIsDefinitive(t *testing.T) {
	track := &models.Track{CleanTitle: "hurt", MusicBrainzTrackID: "mbid-1"}
	results := []SearchResult{
		{Title: "hurt", Album: "The Downward Spiral", Artist: "Nine Inch Nails"},
		{Title: "something else", MBID: "mbid-1"},
	}

	got, ok := BestMatch(track, results, 60)
	if !ok {
		t.Fatal("the MBID result should have matched")
	}
	if got.Title != "something else" {
		t.Fatalf("matched %q, want the MBID result", got.Title)
	}
}
