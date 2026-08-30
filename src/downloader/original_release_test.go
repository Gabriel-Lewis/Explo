package downloader

import (
	"strings"
	"testing"

	cfg "explo/src/config"
	"explo/src/models"
)

// numberedDir builds a candidate release from filenames, with the primary
// pointed at whichever file names the recommended track.
func numberedDir(primaryTitle string, names ...string) peerDir {
	dir := peerDir{dir: "Billie Eilish - When We All Fall Asleep"}
	for _, name := range names {
		dir.files = append(dir.files, File{
			Name:      name,
			Extension: "flac",
			BitRate:   900,
		})
	}
	for i := range dir.files {
		if strings.Contains(string(dir.files[i].Name), primaryTitle) {
			dir.primary = &dir.files[i]
			break
		}
	}
	return dir
}

func fileNames(dir peerDir) []string {
	names := make([]string, 0, len(dir.files))
	for _, f := range dir.files {
		names = append(names, string(f.Name))
	}
	return names
}

func TestParseNumbering(t *testing.T) {
	cases := []struct {
		name  string
		disc  int
		track int
		ok    bool
	}{
		// The four shapes that actually turned up in production logs.
		{"The 1975_The 1975_01-04_Chocolate.mp3", 1, 4, true},
		{"1-09 In My Room.flac", 1, 9, true},
		{"0101 - Colors.flac", 1, 1, true},
		{"02 - Sign of the Times.mp3", 0, 2, true},

		{"Hayley Williams_Ego Death_18_Parachute.flac", 0, 18, true},
		{"05-gracie_abrams-look_at_my_life.mp3", 0, 5, true},

		// A year must never be read as a disc marker.
		{"1975-04 Chocolate.mp3", 0, 0, false},
		{"2015 - Badlands.flac", 0, 0, false},

		// Nothing parseable: a peer whose naming template never expanded.
		{"{track-number:02d} - Cowgirl.mp3", 0, 0, false},
		{"Sundown-HUNTR.flac", 0, 0, false},
	}

	for _, tc := range cases {
		got := parseNumbering(tc.name)
		if got.ok != tc.ok || got.disc != tc.disc || got.track != tc.track {
			t.Errorf("parseNumbering(%q) = disc %d track %d ok %v, want disc %d track %d ok %v",
				tc.name, got.disc, got.track, got.ok, tc.disc, tc.track, tc.ok)
		}
	}
}

// The case that prompted this: a peer flattening several discs into one folder,
// which then outscored every honest copy of the album on file count alone.
func TestTrimToOriginal_DropsFlattenedExtraDiscs(t *testing.T) {
	dir := numberedDir("Bad Guy",
		"01-01 Bury A Friend.flac", "01-02 Bad Guy.flac", "01-03 Xanny.flac",
		"02-01 Demo Take.flac", "02-02 Alternate Mix.flac",
	)

	trimToOriginal(&dir, models.Track{CleanTitle: "Bad Guy", TrackTotal: 3, DiscTotal: 1})

	if len(dir.files) != 3 {
		t.Fatalf("kept %d files %v, want the 3 on disc one", len(dir.files), fileNames(dir))
	}
	for _, name := range fileNames(dir) {
		if strings.HasPrefix(name, "02-") {
			t.Errorf("kept %q from the second disc", name)
		}
	}
}

// A real double album is not bloat. MusicBrainz is the arbiter, and when it
// says two discs, both stay.
func TestTrimToOriginal_KeepsARealDoubleAlbum(t *testing.T) {
	dir := numberedDir("Bad Guy",
		"01-01 Bury A Friend.flac", "01-02 Bad Guy.flac",
		"02-01 Xanny.flac", "02-02 Ilomilo.flac",
	)

	trimToOriginal(&dir, models.Track{CleanTitle: "Bad Guy", TrackTotal: 2, DiscTotal: 2})

	if len(dir.files) != 4 {
		t.Fatalf("kept %d files %v, want all 4 of a genuine 2-disc release",
			len(dir.files), fileNames(dir))
	}
}

// With no MusicBrainz answer the assumption is one disc: a peer flattening
// discs together is far more common than an unenriched double album.
func TestTrimToOriginal_TrimsToDiscOneWhenMusicBrainzIsSilent(t *testing.T) {
	dir := numberedDir("Bad Guy",
		"01-01 Bury A Friend.flac", "01-02 Bad Guy.flac",
		"02-01 Xanny.flac", "02-02 Ilomilo.flac",
	)

	trimToOriginal(&dir, models.Track{CleanTitle: "Bad Guy"})

	if len(dir.files) != 2 {
		t.Fatalf("kept %d files %v, want disc one only", len(dir.files), fileNames(dir))
	}
}

// TrackTotal counts the first medium only, so measuring a legitimately kept
// two-disc release against it would amputate the second disc for existing.
func TestTrimToOriginal_SkipsTheBonusPassOnAKeptDoubleAlbum(t *testing.T) {
	dir := numberedDir("Bad Guy",
		"01-01 Bury A Friend.flac", "01-02 Bad Guy.flac", "01-03 Xanny.flac",
		"02-01 Ilomilo.flac", "02-02 8.flac", "02-03 My Strange Addiction.flac",
	)

	trimToOriginal(&dir, models.Track{CleanTitle: "Bad Guy", TrackTotal: 3, DiscTotal: 2})

	if len(dir.files) != 6 {
		t.Fatalf("kept %d files %v, want all 6: TrackTotal describes disc one, not the release",
			len(dir.files), fileNames(dir))
	}
}

// The Halsey case: a single-disc anthology padded past the album's real length.
func TestTrimToOriginal_DropsBonusTracks(t *testing.T) {
	dir := numberedDir("Bad Guy",
		"01 Bury A Friend.flac", "02 Bad Guy.flac", "03 Xanny.flac",
		"04 Bonus Demo.flac", "05 Bonus Live Take.flac",
	)

	trimToOriginal(&dir, models.Track{CleanTitle: "Bad Guy", TrackTotal: 3, DiscTotal: 1})

	if len(dir.files) != 3 {
		t.Fatalf("kept %d files %v, want the 3-track album", len(dir.files), fileNames(dir))
	}
	for _, name := range fileNames(dir) {
		if strings.Contains(name, "Bonus") {
			t.Errorf("kept bonus material %q", name)
		}
	}
}

// Trimming away the recommended track would lose the playlist entry, which is
// worse than downloading one file more than intended.
func TestTrimToOriginal_AlwaysKeepsThePrimary(t *testing.T) {
	dir := numberedDir("Bad Guy",
		"01 Bury A Friend.flac", "02 Xanny.flac", "03 Ilomilo.flac",
		"04 Bonus Demo.flac", "05 Bad Guy.flac",
	)

	trimToOriginal(&dir, models.Track{CleanTitle: "Bad Guy", TrackTotal: 3, DiscTotal: 1})

	var found bool
	for _, name := range fileNames(dir) {
		if strings.Contains(name, "Bad Guy") {
			found = true
		}
	}
	if !found {
		t.Fatalf("trimmed away the recommended track; kept %v", fileNames(dir))
	}
	if dir.primary == nil || !strings.Contains(string(dir.primary.Name), "Bad Guy") {
		t.Errorf("primary = %v, want it still pointing at the recommended track", dir.primary)
	}
}

// dir.primary points into the files slice. Trimming reallocates it, so a
// pointer left behind would read a file no longer in the release.
func TestTrimToOriginal_ReseatsThePrimaryPointer(t *testing.T) {
	dir := numberedDir("Bad Guy",
		"01-01 Bury A Friend.flac", "01-02 Bad Guy.flac",
		"02-01 Demo Take.flac",
	)

	trimToOriginal(&dir, models.Track{CleanTitle: "Bad Guy", TrackTotal: 2, DiscTotal: 1})

	if dir.primary == nil {
		t.Fatal("primary = nil after trimming")
	}
	var inSlice bool
	for i := range dir.files {
		if &dir.files[i] == dir.primary {
			inSlice = true
		}
	}
	if !inSlice {
		t.Error("primary points outside the trimmed release; it was not re-seated")
	}
}

// Cutting a directory we cannot read the numbering of would be cutting at
// random. A peer sharing an unexpanded naming template is not hypothetical.
func TestTrimToOriginal_SkipsUnparseableDirectories(t *testing.T) {
	dir := numberedDir("Bad Guy",
		"Bury A Friend.flac", "Bad Guy.flac", "Xanny.flac",
		"{track-number:02d} - Ilomilo.flac", "Goodbye.flac",
	)

	trimToOriginal(&dir, models.Track{CleanTitle: "Bad Guy", TrackTotal: 3, DiscTotal: 1})

	if len(dir.files) != 5 {
		t.Fatalf("kept %d files %v, want all 5 left alone", len(dir.files), fileNames(dir))
	}
}

func TestLaterThanOriginal(t *testing.T) {
	cases := []struct {
		dir  string
		year int
		want bool
	}{
		{"Halsey/2023 - Badlands (Remastered)", 2015, true},
		{"Halsey/2015 - Badlands", 2015, false},
		{"Halsey/Badlands", 2015, false},
		{"Halsey/2015 - Badlands (Decade Edition Anthology)", 2015, false},
		{"Halsey/2023 - Badlands", 0, false},
		// A year in a parent folder describes the peer's library, not this
		// release, so only the final segment is read.
		{"@@x/2023 Rips/Halsey/2015 - Badlands", 2015, false},
	}

	for _, tc := range cases {
		got := laterThanOriginal(tc.dir, models.Track{OriginalYear: tc.year})
		if got != tc.want {
			t.Errorf("laterThanOriginal(%q, %d) = %v, want %v", tc.dir, tc.year, got, tc.want)
		}
	}
}

// originalClient mirrors albumClient with the trim switched on.
func originalClient(t *testing.T, prefer bool) *Slskd {
	t.Helper()

	client := NewSlskd(cfg.Slskd{
		URL:                   "http://localhost",
		APIKey:                "test-key",
		Timeout:               5,
		AlbumMode:             true,
		PreferOriginalRelease: prefer,
		Filters: cfg.Filters{
			Extensions:  []string{"flac", "mp3"},
			MinBitRate:  256,
			MinBitDepth: 8,
			FilterList:  []string{"live", "remix"},
		},
	}, "/data/")
	client.AddHeader()

	return client
}

func flattenedTrack() models.Track {
	track := albumTrack()
	track.TrackTotal = 3
	track.DiscTotal = 1
	return track
}

func flattenedResults() SearchResults {
	return albumResults(peer{"peer1", `@@x\Billie Eilish\When We All Fall Asleep`, []string{
		"01-01 Bury A Friend.flac", "01-02 Bad Guy.flac", "01-03 Xanny.flac",
		"02-01 Demo Take.flac", "02-02 Alternate Mix.flac",
	}})
}

func TestCollectAlbumFiles_TrimsAFlattenedReleaseToTheAlbum(t *testing.T) {
	files, err := originalClient(t, true).CollectAlbumFiles(flattenedTrack(), flattenedResults())
	if err != nil {
		t.Fatalf("CollectAlbumFiles() = %v, want nil", err)
	}
	if len(files) != 3 {
		t.Fatalf("queued %d files, want the 3 on disc one", len(files))
	}
	if !strings.Contains(string(files[0].Name), "Bad Guy") {
		t.Errorf("files[0] = %q, want the recommended track first", files[0].Name)
	}
}

func TestCollectAlbumFiles_LeavesReleasesAloneWhenDisabled(t *testing.T) {
	files, err := originalClient(t, false).CollectAlbumFiles(flattenedTrack(), flattenedResults())
	if err != nil {
		t.Fatalf("CollectAlbumFiles() = %v, want nil", err)
	}
	if len(files) != 5 {
		t.Fatalf("queued %d files with the setting off, want all 5 untouched", len(files))
	}
}

// The hole PR #19 left open. ListenBrainz resolved the recording to a two-disc
// deluxe, so DiscTotal says 2 and the flattened directory was kept whole -- the
// exact failure the feature exists to prevent. The group's consensus knows the
// album is single-disc.
func TestTrimToOriginal_ConsensusBeatsADeluxeMatch(t *testing.T) {
	dir := numberedDir("Bad Guy",
		"01-01 Bury A Friend.flac", "01-02 Bad Guy.flac", "01-03 Xanny.flac",
		"02-01 Demo Take.flac", "02-02 Alternate Mix.flac",
	)

	trimToOriginal(&dir, models.Track{
		CleanTitle:          "Bad Guy",
		TrackTotal:          23, // the deluxe's length
		DiscTotal:           2,  // the deluxe's disc count
		CanonicalTrackTotal: 3,
		CanonicalDiscTotal:  1,
	})

	if len(dir.files) != 3 {
		t.Fatalf("kept %d files %v, want the 3 on disc one", len(dir.files), fileNames(dir))
	}
}

// A genuine double album has a group that agrees it is one, so consensus
// winning must not amputate it.
func TestTrimToOriginal_ConsensusKeepsAGenuineDoubleAlbum(t *testing.T) {
	dir := numberedDir("Bad Guy",
		"01-01 Bury A Friend.flac", "01-02 Bad Guy.flac",
		"02-01 Xanny.flac", "02-02 Ilomilo.flac",
	)

	trimToOriginal(&dir, models.Track{
		CleanTitle:          "Bad Guy",
		CanonicalTrackTotal: 2,
		CanonicalDiscTotal:  2,
	})

	if len(dir.files) != 4 {
		t.Fatalf("kept %d files %v, want all 4", len(dir.files), fileNames(dir))
	}
}

// Numbering that reads cleanly but repeats is describing something other than
// one release. Cutting on it would drop the wrong files.
func TestTrimToOriginal_SkipsWhenNumberingRepeats(t *testing.T) {
	dir := numberedDir("Bad Guy",
		"01 Bury A Friend.flac", "02 Bad Guy.flac", "03 Xanny.flac",
		"01 Bury A Friend (Remaster).flac", "02 Bad Guy (Remaster).flac",
	)

	trimToOriginal(&dir, models.Track{CleanTitle: "Bad Guy", CanonicalTrackTotal: 3, CanonicalDiscTotal: 1})

	if len(dir.files) != 5 {
		t.Fatalf("kept %d files %v, want all 5 left alone", len(dir.files), fileNames(dir))
	}
}

func TestExpectedAlbumLength_PrefersTheConsensus(t *testing.T) {
	got := expectedAlbumLength(models.Track{TrackTotal: 23, CanonicalTrackTotal: 16})
	if got != 16 {
		t.Errorf("expectedAlbumLength() = %d, want the consensus 16", got)
	}
	if got := expectedAlbumLength(models.Track{TrackTotal: 23}); got != 23 {
		t.Errorf("without a consensus = %d, want the matched release's 23", got)
	}
}

// Everything above must be inert with enrichment off, leaving PR #19's
// behaviour byte for byte.
func TestTrimToOriginal_UnchangedWithoutAConsensus(t *testing.T) {
	dir := numberedDir("Bad Guy",
		"01-01 Bury A Friend.flac", "01-02 Bad Guy.flac",
		"02-01 Xanny.flac", "02-02 Ilomilo.flac",
	)

	trimToOriginal(&dir, models.Track{CleanTitle: "Bad Guy", TrackTotal: 2, DiscTotal: 2})

	if len(dir.files) != 4 {
		t.Fatalf("kept %d files %v, want DiscTotal still honoured with no consensus",
			len(dir.files), fileNames(dir))
	}
}
