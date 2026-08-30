package downloader

import (
	"testing"

	cfg "explo/src/config"
	"explo/src/models"
)

func prefClient(pref string) Slskd {
	return Slskd{Cfg: cfg.Slskd{
		DownloadAttempts: 10,
		SizePreference:   pref,
		Filters: cfg.Filters{
			Extensions:  []string{"flac", "mp3"},
			MinBitDepth: 8,
		},
	}}
}

func names(files []File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The candidates every ordering test starts from, deliberately not in any
// meaningful order.
func candidates() []File {
	return []File{
		{Name: "mp3-256", Extension: "mp3", BitRate: 256},
		{Name: "flac-cd", Extension: "flac", BitRate: 1000},
		{Name: "mp3-320", Extension: "mp3", BitRate: 320},
		{Name: "flac-hi", Extension: "flac", BitRate: 4608},
	}
}

func TestSortByPreference(t *testing.T) {
	tests := []struct {
		name string
		pref string
		want []string
	}{
		{
			// The point of "smaller": the best lossy file, not the smallest
			// file. 320 beats 256, and both beat any FLAC.
			name: "smaller takes the best lossy first",
			pref: PreferSmaller,
			want: []string{"mp3-320", "mp3-256", "flac-hi", "flac-cd"},
		},
		{
			name: "larger takes the best lossless first",
			pref: PreferLarger,
			want: []string{"flac-hi", "flac-cd", "mp3-320", "mp3-256"},
		},
		{
			// Unchanged behaviour: EXTENSIONS is flac,mp3, so FLAC first, and
			// within a format the order the search returned them in.
			name: "none follows the extension order",
			pref: PreferNone,
			want: []string{"flac-cd", "flac-hi", "mp3-256", "mp3-320"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := prefClient(tt.pref)
			files := candidates()
			client.sortByPreference(files)

			if got := names(files); !equal(got, tt.want) {
				t.Errorf("order = %v, want %v", got, tt.want)
			}
		})
	}
}

// The preference ranks, it does not filter: when the preferred band is empty
// the other one must still be reached rather than the track being lost.
func TestSortByPreference_FallsBackWhenTheBandIsEmpty(t *testing.T) {
	client := prefClient(PreferSmaller)
	files := []File{
		{Name: "flac-cd", Extension: "flac", BitRate: 1000},
		{Name: "flac-hi", Extension: "flac", BitRate: 4608},
	}

	client.sortByPreference(files)

	if len(files) != 2 {
		t.Fatalf("kept %d files, want both -- preference must not drop candidates", len(files))
	}
	if files[0].Name != "flac-hi" {
		t.Errorf("first = %s, want the best of the only band available", files[0].Name)
	}
}

// A file nobody described is still eligible, but it cannot be called the best
// of its band.
func TestSortByPreference_UnknownBitrateRanksLastInItsBand(t *testing.T) {
	client := prefClient(PreferSmaller)
	files := []File{
		{Name: "mp3-unknown", Extension: "mp3"},
		{Name: "mp3-256", Extension: "mp3", BitRate: 256},
		{Name: "flac-cd", Extension: "flac", BitRate: 1000},
	}

	client.sortByPreference(files)

	want := []string{"mp3-256", "mp3-unknown", "flac-cd"}
	if got := names(files); !equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// filterFiles caps at DownloadAttempts, so the ordering decides which
// candidates survive, not just what order they are tried in.
func TestFilterFiles_PreferenceDecidesWhichSurviveTheCap(t *testing.T) {
	client := prefClient(PreferSmaller)
	client.Cfg.DownloadAttempts = 1

	got, err := client.filterFiles(candidates())
	if err != nil {
		t.Fatalf("filterFiles() = %v, want nil", err)
	}
	if len(got) != 1 || got[0].Name != "mp3-320" {
		t.Errorf("kept %v, want just mp3-320", names(got))
	}
}

func TestReleaseBitRate_MedianIgnoresAnOutlier(t *testing.T) {
	files := []File{
		{BitRate: 320}, {BitRate: 320}, {BitRate: 320}, {BitRate: 320},
		{BitRate: 96}, // a bonus track nobody cares about
	}

	if got := releaseBitRate(files); got != 320 {
		t.Errorf("releaseBitRate() = %d, want 320 -- one bad track must not sink the release", got)
	}
}

func TestReleaseBitRate_UnknownWhenNothingIsDescribed(t *testing.T) {
	if got := releaseBitRate([]File{{}, {}}); got != 0 {
		t.Errorf("releaseBitRate() = %d, want 0", got)
	}
}

func albumDir(ext string, rate int, names ...string) peerDir {
	dir := peerDir{dir: "Billie Eilish - When We All Fall Asleep"}
	for _, n := range names {
		dir.files = append(dir.files, File{Name: n, Extension: ext, BitRate: rate})
	}
	return dir
}

// Quality is a tiebreak between equally good matches. It must never be a
// reason to take a release that matches the recommendation worse.
func TestScoreDirWithPreference_QualityNeverBeatsAMatch(t *testing.T) {
	track := models.Track{
		CleanTitle: "Bad Guy",
		MainArtist: "Billie Eilish",
		Album:      "When We All Fall Asleep",
	}

	client := prefClient(PreferLarger)

	right := albumDir("mp3", 320, "01 Bad Guy.mp3")
	right.dir = "Billie Eilish - When We All Fall Asleep"

	wrong := albumDir("flac", 1000, "01 Bad Guy.flac")
	wrong.dir = "Some Other Compilation"

	rightScore := client.scoreDirWithPreference(right, track)
	wrongScore := client.scoreDirWithPreference(wrong, track)

	if wrongScore >= rightScore {
		t.Errorf("wrong-album FLAC scored %d against right-album MP3 %d; quality outranked the match",
			wrongScore, rightScore)
	}
}

// Between two releases that match equally well, the preference decides.
func TestScoreDirWithPreference_BreaksATieBetweenEqualMatches(t *testing.T) {
	track := models.Track{
		CleanTitle: "Bad Guy",
		MainArtist: "Billie Eilish",
		Album:      "When We All Fall Asleep",
	}

	lossy := albumDir("mp3", 320, "01 Bad Guy.mp3")
	lossless := albumDir("flac", 1000, "01 Bad Guy.flac")

	smaller := prefClient(PreferSmaller)
	if smaller.scoreDirWithPreference(lossy, track) <= smaller.scoreDirWithPreference(lossless, track) {
		t.Error("prefer smaller did not favour the lossy release")
	}

	larger := prefClient(PreferLarger)
	if larger.scoreDirWithPreference(lossless, track) <= larger.scoreDirWithPreference(lossy, track) {
		t.Error("prefer larger did not favour the lossless release")
	}
}

// The default has to leave release scoring exactly as it was.
func TestScoreDirWithPreference_NoneChangesNothing(t *testing.T) {
	track := models.Track{
		CleanTitle: "Bad Guy",
		MainArtist: "Billie Eilish",
		Album:      "When We All Fall Asleep",
	}

	client := prefClient(PreferNone)
	for _, dir := range []peerDir{
		albumDir("mp3", 320, "01 Bad Guy.mp3"),
		albumDir("flac", 1000, "01 Bad Guy.flac"),
	} {
		if got, want := client.scoreDirWithPreference(dir, track), scoreDir(dir, track); got != want {
			t.Errorf("with no preference score = %d, want the unmodified %d", got, want)
		}
	}
}

func TestNormalisePreference(t *testing.T) {
	cases := map[string]string{
		"smaller": PreferSmaller,
		"LARGER":  PreferLarger,
		" none ":  PreferNone,
		"":        PreferNone,
		"nonsense": PreferNone,
	}
	for in, want := range cases {
		if got := normalisePreference(in); got != want {
			t.Errorf("normalisePreference(%q) = %q, want %q", in, got, want)
		}
	}
}
