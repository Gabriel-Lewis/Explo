package downloader

import (
	"testing"

	cfg "explo/src/config"
	"explo/src/models"
)

func TestEffectiveBitRate(t *testing.T) {
	tests := []struct {
		name string
		file File
		want int
	}{
		{
			// A file that describes itself is judged exactly as it was before.
			name: "reported bitrate wins",
			file: File{BitRate: 320, Size: 99_999_999, Length: 200},
			want: 320,
		},
		{
			// 210 s of 320 kbps is about 8.4 MB.
			name: "derived from size and length when unreported",
			file: File{Size: 8_400_000, Length: 210},
			want: 320,
		},
		{
			name: "derived for a CD flac",
			file: File{Size: 26_000_000, Length: 210},
			want: 990,
		},
		{
			name: "unknown without a length",
			file: File{Size: 8_400_000},
			want: 0,
		},
		{
			name: "unknown without a size",
			file: File{Length: 210},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveBitRate(tt.file)
			// Derivation is integer division, so allow a kbps either way.
			if got < tt.want-2 || got > tt.want+2 {
				t.Errorf("effectiveBitRate(%+v) = %d, want about %d", tt.file, got, tt.want)
			}
		})
	}
}

func TestIsLossless(t *testing.T) {
	for _, ext := range []string{"flac", "FLAC", ".flac", "wav", "aiff", "alac"} {
		if !isLossless(File{Extension: ext}) {
			t.Errorf("isLossless(%q) = false, want true", ext)
		}
	}
	for _, ext := range []string{"mp3", "m4a", "aac", "ogg", ""} {
		if isLossless(File{Extension: ext}) {
			t.Errorf("isLossless(%q) = true, want false", ext)
		}
	}
}

func rangeClient(minRate, maxRate int) Slskd {
	return Slskd{Cfg: cfg.Slskd{
		DownloadAttempts: 10,
		Filters: cfg.Filters{
			Extensions:  []string{"flac", "mp3"},
			MinBitRate:  minRate,
			MaxBitRate:  maxRate,
			MinBitDepth: 8,
		},
	}}
}

func TestWithinQualityRange(t *testing.T) {
	tests := []struct {
		name    string
		client  Slskd
		file    File
		wantIn  bool
	}{
		{"inside the range", rangeClient(192, 320), File{BitRate: 256}, true},
		{"below the floor", rangeClient(192, 320), File{BitRate: 128}, false},
		{"above the ceiling", rangeClient(192, 320), File{BitRate: 1000}, false},
		{"at the ceiling", rangeClient(192, 320), File{BitRate: 320}, true},
		{"at the floor", rangeClient(192, 320), File{BitRate: 192}, true},
		{
			// The default: no ceiling configured, so nothing changes for
			// anyone who has not set one.
			name:   "a zero ceiling rejects nothing",
			client: rangeClient(192, 0),
			file:   File{BitRate: 4608},
			wantIn: true,
		},
		{
			// Most of Soulseek reports nothing. Dropping all of it would leave
			// almost no candidates.
			name:   "unknown bitrate passes the ceiling",
			client: rangeClient(192, 320),
			file:   File{},
			wantIn: true,
		},
		{
			// Derived from size, so the ceiling reaches files the old
			// reported-only checks skipped entirely.
			name:   "a big unreported file is caught by derivation",
			client: rangeClient(192, 320),
			file:   File{Size: 26_000_000, Length: 210},
			wantIn: false,
		},
		{"bit depth below the floor", rangeClient(0, 0), File{BitDepth: 4, BitRate: 256}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.client.withinQualityRange(tt.file); got != tt.wantIn {
				t.Errorf("withinQualityRange(%+v) = %v, want %v", tt.file, got, tt.wantIn)
			}
		})
	}
}

// filterFiles is the single-track path.
func TestFilterFiles_AppliesTheCeiling(t *testing.T) {
	client := rangeClient(192, 320)

	files := []File{
		{Name: "big.flac", Extension: "flac", BitRate: 1000},
		{Name: "ok.mp3", Extension: "mp3", BitRate: 320},
	}

	got, err := client.filterFiles(files)
	if err != nil {
		t.Fatalf("filterFiles() = %v, want nil", err)
	}
	if len(got) != 1 || string(got[0].Name) != "ok.mp3" {
		t.Errorf("kept %v, want only ok.mp3", got)
	}
}

func TestFilterFiles_NoCeilingKeepsEverything(t *testing.T) {
	client := rangeClient(192, 0)

	files := []File{
		{Name: "big.flac", Extension: "flac", BitRate: 4608},
		{Name: "ok.mp3", Extension: "mp3", BitRate: 320},
	}

	got, err := client.filterFiles(files)
	if err != nil {
		t.Fatalf("filterFiles() = %v, want nil", err)
	}
	if len(got) != 2 {
		t.Errorf("kept %d files, want both", len(got))
	}
}

// The album path exempts the recommended track from the quality floors, on the
// grounds that a mediocre entry beats none. The ceiling is a different
// question -- it is about transfer volume -- so the primary must not be exempt
// from it.
func TestQualityFiltered_CeilingAppliesToThePrimaryToo(t *testing.T) {
	client := rangeClient(192, 320)

	primary := File{Name: "01 primary.flac", Extension: "flac", BitRate: 1000}
	dir := peerDir{
		files:   []File{primary, {Name: "02 sibling.mp3", Extension: "mp3", BitRate: 320}},
		primary: &primary,
	}

	kept := client.qualityFiltered(dir)
	for _, f := range kept {
		if string(f.Name) == "01 primary.flac" {
			t.Error("the primary was kept despite being over the ceiling")
		}
	}
}

// The floor exemption for the primary has to survive, or album mode loses the
// playlist entry it exists to produce.
func TestQualityFiltered_PrimaryStillExemptFromTheFloor(t *testing.T) {
	client := rangeClient(320, 0)

	primary := File{Name: "01 primary.mp3", Extension: "mp3", BitRate: 128}
	dir := peerDir{
		files:   []File{primary, {Name: "02 sibling.mp3", Extension: "mp3", BitRate: 128}},
		primary: &primary,
	}

	kept := client.qualityFiltered(dir)
	if len(kept) != 1 || string(kept[0].Name) != "01 primary.mp3" {
		t.Errorf("kept %v, want just the primary -- it is exempt from the floor, siblings are not", kept)
	}
}

// Guard against the album flow silently ignoring the new setting.
func TestCollectAlbumFiles_RespectsTheCeiling(t *testing.T) {
	client := rangeClient(0, 320)
	client.Cfg.AlbumMode = true

	results := albumResults(peer{"peer1", `@@x\Billie Eilish\When We All Fall Asleep`,
		[]string{"01 Bury A Friend.flac", "02 Bad Guy.flac"}})
	// albumResults leaves BitRate unset, so give the files a size that derives
	// well above the ceiling.
	for i := range results {
		for j := range results[i].Files {
			results[i].Files[j].Size = 26_000_000
			results[i].Files[j].Length = 210
		}
	}

	track := models.Track{
		CleanTitle: "Bad Guy",
		Artist:     "Billie Eilish",
		MainArtist: "Billie Eilish",
		Album:      "When We All Fall Asleep",
	}

	if _, err := client.CollectAlbumFiles(track, results); err == nil {
		t.Error("CollectAlbumFiles() = nil error, want a failure when every file is over the ceiling")
	}
}
