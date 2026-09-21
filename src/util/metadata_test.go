package util

import (
	"strings"
	"testing"

	"explo/src/models"
)

// fullyTaggedTrack populates every field the tag maps can carry, so a
// container that has no name for one of them has something to get wrong.
func fullyTaggedTrack(file string) models.Track {
	return models.Track{
		File:                      file,
		Title:                     "Hurt",
		Album:                     "The Downward Spiral",
		Artist:                    "Nine Inch Nails",
		AlbumArtist:               "Nine Inch Nails",
		ArtistSort:                "Nine Inch Nails",
		OriginalDate:              "1994-03-08",
		OriginalYear:              1994,
		Genres:                    "Industrial",
		Media:                     "CD",
		ReleaseType:               "album",
		ReleaseStatus:             "official",
		ISRCs:                     []string{"USIR19400821"},
		TrackNumber:               13,
		TrackTotal:                14,
		DiscNumber:                1,
		DiscTotal:                 1,
		MusicBrainzTrackID:        "track-mbid",
		MusicBrainzAlbumID:        "album-mbid",
		MusicBrainzArtistID:       "artist-mbid",
		MusicBrainzAlbumArtistID:  "album-artist-mbid",
		MusicBrainzReleaseGroupID: "release-group-mbid",
		MusicBrainzReleaseTrackID: "release-track-mbid",
	}
}

// A tag map that has no name for a field leaves the key empty, and only the
// value was checked before writing -- so every populated field the container
// cannot express was emitted as a nameless "=value" entry.
func TestBuildffmpegMetadata_NeverWritesANamelessTag(t *testing.T) {
	for _, file := range []string{"a.mp3", "a.flac", "a.opus", "a.ape", "a.wv", "a.mpc", "a.m4a", "a.wav", "a.aac"} {
		t.Run(file, func(t *testing.T) {
			for _, entry := range BuildffmpegMetadata(fullyTaggedTrack(file)) {
				if strings.HasPrefix(entry, "=") {
					t.Errorf("emitted nameless tag %q", entry)
				}
			}
		})
	}
}

// The guard must not hollow out the fallback: the tags it does name are still
// written.
func TestBuildffmpegMetadata_FallbackStillWritesTheTagsItNames(t *testing.T) {
	got := BuildffmpegMetadata(fullyTaggedTrack("a.m4a"))

	for _, want := range []string{
		"title=Hurt",
		"album=The Downward Spiral",
		"artist=Nine Inch Nails",
		"track=13",
		"disc=1",
	} {
		if !contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
}

// Ogg Vorbis carries vorbis comments, so it belongs with flac and opus rather
// than in the stripped-down fallback.
func TestBuildffmpegMetadata_OggUsesVorbisComments(t *testing.T) {
	got := BuildffmpegMetadata(fullyTaggedTrack("a.ogg"))

	if !contains(got, "MusicBrainz_TrackId=track-mbid") {
		t.Errorf("ogg did not get vorbis naming: %v", got)
	}
}

// The containers upstream already mapped keep their naming.
func TestBuildffmpegMetadata_KeepsTheEstablishedNaming(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"a.mp3", "MusicBrainz Track Id=track-mbid"},
		{"a.flac", "MusicBrainz_TrackId=track-mbid"},
		{"a.opus", "MusicBrainz_TrackId=track-mbid"},
		{"a.ape", "MusicBrainz_TrackId=track-mbid"},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			if got := BuildffmpegMetadata(fullyTaggedTrack(tt.file)); !contains(got, tt.want) {
				t.Errorf("missing %q in %v", tt.want, got)
			}
		})
	}
}

// An empty value is still skipped, named or not.
func TestBuildffmpegMetadata_SkipsEmptyValues(t *testing.T) {
	got := BuildffmpegMetadata(models.Track{File: "a.flac", Title: "Hurt"})

	for _, entry := range got {
		if strings.HasSuffix(entry, "=") || strings.HasPrefix(entry, "=") {
			t.Errorf("emitted empty tag %q", entry)
		}
	}
	if !contains(got, "title=Hurt") {
		t.Errorf("missing title in %v", got)
	}
}

func contains(entries []string, want string) bool {
	for _, entry := range entries {
		if entry == want {
			return true
		}
	}
	return false
}
