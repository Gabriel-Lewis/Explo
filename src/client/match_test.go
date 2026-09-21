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
