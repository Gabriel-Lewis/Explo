package discovery

import (
	"encoding/json"
	"testing"
)

const groupID = "group-1"

// release builds one release in groupID. Each entry in mediaTracks is one
// medium's track count, so len(mediaTracks) is the disc count.
func release(status string, format string, mediaTracks ...int) MBRelease {
	rel := MBRelease{Status: status}
	rel.ReleaseGroup.ID = groupID
	for _, count := range mediaTracks {
		rel.Media = append(rel.Media, MBMedium{Format: format, TrackCount: count})
	}
	return rel
}

// The point of the whole exercise: the matched release may be a deluxe, and the
// group knows better.
func TestCanonicalReleaseShape_PrefersTheConsensusOverAnOutlier(t *testing.T) {
	releases := []MBRelease{
		release("Official", "CD", 16),
		release("Official", "CD", 16),
		release("Official", "Digital Media", 16),
		release("Official", "CD", 16, 23), // the two-disc deluxe
	}

	tracks, discs := CanonicalReleaseShape(releases, groupID)

	if tracks != 16 || discs != 1 {
		t.Errorf("CanonicalReleaseShape() = %d tracks, %d discs; want 16 and 1", tracks, discs)
	}
}

// A real double album has a group that agrees it is one.
func TestCanonicalReleaseShape_KeepsAGenuineDoubleAlbum(t *testing.T) {
	releases := []MBRelease{
		release("Official", "CD", 13, 13),
		release("Official", "CD", 13, 13),
		release("Official", "CD", 26),
	}

	if _, discs := CanonicalReleaseShape(releases, groupID); discs != 2 {
		t.Errorf("disc consensus = %d, want 2", discs)
	}
}

// A recording appears on greatest hits and soundtracks too, and their lengths
// say nothing about this album.
func TestCanonicalReleaseShape_IgnoresOtherReleaseGroups(t *testing.T) {
	other := release("Official", "CD", 40)
	other.ReleaseGroup.ID = "group-2"

	releases := []MBRelease{
		release("Official", "CD", 11),
		other, other, other,
	}

	if tracks, _ := CanonicalReleaseShape(releases, groupID); tracks != 11 {
		t.Errorf("track consensus = %d, want 11 from this group only", tracks)
	}
}

func TestCanonicalReleaseShape_IgnoresBootlegsWhenOfficialsExist(t *testing.T) {
	releases := []MBRelease{
		release("Official", "CD", 12),
		release("Bootleg", "CD", 30),
		release("Bootleg", "CD", 30),
		release("Bootleg", "CD", 30),
	}

	if tracks, _ := CanonicalReleaseShape(releases, groupID); tracks != 12 {
		t.Errorf("track consensus = %d, want the official 12 despite more bootlegs", tracks)
	}
}

// Poor evidence beats none: a group with nothing official still yields numbers
// rather than falling back to the matched release.
func TestCanonicalReleaseShape_UsesBootlegsWhenThatIsAllThereIs(t *testing.T) {
	releases := []MBRelease{
		release("Bootleg", "CD", 9),
		release("Bootleg", "CD", 9),
	}

	if tracks, _ := CanonicalReleaseShape(releases, groupID); tracks != 9 {
		t.Errorf("track consensus = %d, want 9", tracks)
	}
}

// A 2LP pressing of a single-CD album lists two media and would drag the disc
// consensus up to two.
func TestCanonicalReleaseShape_ExcludesVinylFromTheDiscCount(t *testing.T) {
	releases := []MBRelease{
		release("Official", "CD", 14),
		release("Official", "Vinyl", 7, 7),
		release("Official", "Vinyl", 7, 7),
	}

	tracks, discs := CanonicalReleaseShape(releases, groupID)

	if discs != 1 {
		t.Errorf("disc consensus = %d, want 1; the vinyl pressings inflated it", discs)
	}
	if tracks != 14 {
		t.Errorf("track consensus = %d, want the CD's 14", tracks)
	}
}

// The tie-break is the point: asked for an album shared equally as original and
// expanded, the original is the one wanted.
func TestCanonicalReleaseShape_BreaksTiesTowardTheSmaller(t *testing.T) {
	releases := []MBRelease{
		release("Official", "CD", 12),
		release("Official", "CD", 20),
	}

	if tracks, _ := CanonicalReleaseShape(releases, groupID); tracks != 12 {
		t.Errorf("track consensus = %d, want the smaller 12 on a tie", tracks)
	}
}

func TestCanonicalReleaseShape_ZeroWithoutUsableReleases(t *testing.T) {
	cases := map[string]struct {
		releases []MBRelease
		group    string
	}{
		"no releases":     {nil, groupID},
		"no group id":     {[]MBRelease{release("Official", "CD", 12)}, ""},
		"no media":        {[]MBRelease{release("Official", "CD")}, groupID},
		"all other group": {[]MBRelease{{Status: "Official"}}, groupID},
	}

	for name, tc := range cases {
		tracks, discs := CanonicalReleaseShape(tc.releases, tc.group)
		if tracks != 0 || discs != 0 {
			t.Errorf("%s: got %d tracks, %d discs; want zeroes so callers fall back",
				name, tracks, discs)
		}
	}
}

// The consensus is read off a real MusicBrainz payload, so the struct tags have
// to survive. MBRelease and MBMedium were extracted from anonymous structs to
// make this helper testable, and a mistyped tag there would silently yield no
// consensus rather than an error.
func TestCanonicalReleaseShape_FromAMusicBrainzPayload(t *testing.T) {
	const body = `{
	  "id": "rec-1",
	  "releases": [
	    {"id": "r1", "title": "When We All Fall Asleep", "status": "Official",
	     "release-group": {"id": "group-1", "primary-type": "Album"},
	     "media": [{"position": 1, "format": "CD", "track-count": 14}]},
	    {"id": "r2", "title": "When We All Fall Asleep", "status": "Official",
	     "release-group": {"id": "group-1", "primary-type": "Album"},
	     "media": [{"position": 1, "format": "Digital Media", "track-count": 14}]},
	    {"id": "r3", "title": "When We All Fall Asleep (Deluxe)", "status": "Official",
	     "release-group": {"id": "group-1", "primary-type": "Album"},
	     "media": [{"position": 1, "format": "CD", "track-count": 14},
	               {"position": 2, "format": "CD", "track-count": 8}]},
	    {"id": "r4", "title": "Now That's What I Call Music", "status": "Official",
	     "release-group": {"id": "group-2", "primary-type": "Album"},
	     "media": [{"position": 1, "format": "CD", "track-count": 42}]}
	  ]
	}`

	var recording MBRecording
	if err := json.Unmarshal([]byte(body), &recording); err != nil {
		t.Fatalf("decoding the payload: %v", err)
	}
	if len(recording.Releases) != 4 {
		t.Fatalf("decoded %d releases, want 4; the struct tags no longer match",
			len(recording.Releases))
	}

	tracks, discs := CanonicalReleaseShape(recording.Releases, "group-1")

	if tracks != 14 || discs != 1 {
		t.Errorf("CanonicalReleaseShape() = %d tracks, %d discs; want 14 and 1 "+
			"(the deluxe outvoted, the compilation ignored)", tracks, discs)
	}
}
