package downloader

import (
	"cmp"
	"slices"
	"strings"
)

// Size preferences. Ranking, not filtering: everything inside the configured
// quality range stays eligible, the preference only decides what is reached
// for first.
const (
	// PreferNone leaves ordering to the order of EXTENSIONS, which is how
	// Explo has always chosen, so an upgrade changes nothing.
	PreferNone = "none"
	// PreferSmaller takes the best lossy file available and only falls back to
	// lossless when there is no lossy candidate at all. Note that this is not
	// "smallest": within the lossy band it still wants the highest bitrate,
	// which is the whole point -- a 320 beats a 256 beats a FLAC.
	PreferSmaller = "smaller"
	// PreferLarger is the reverse, and reduces to plain highest-bitrate-first.
	PreferLarger = "larger"
)

// normalisePreference tolerates casing and blank values so a hand-edited .env
// cannot break a run over a capital letter.
func normalisePreference(pref string) string {
	switch strings.ToLower(strings.TrimSpace(pref)) {
	case PreferSmaller:
		return PreferSmaller
	case PreferLarger:
		return PreferLarger
	default:
		return PreferNone
	}
}

// bandRank groups a file for the preference. Lower sorts first.
//
// The two settings are not mirror images. "Larger" is satisfied by sorting on
// bitrate alone, while "smaller" needs the band, because wanting a small file
// does not mean wanting a bad one.
func bandRank(file File, pref string) int {
	switch pref {
	case PreferSmaller:
		if isLossless(file) {
			return 1
		}
		return 0
	case PreferLarger:
		if isLossless(file) {
			return 0
		}
		return 1
	default:
		return 0
	}
}

// compareFiles orders two candidates under a preference. Returns a negative
// number when a should be tried before b.
func (c Slskd) compareFiles(a, b File, pref string) int {
	if pref == PreferNone {
		// Preserve the historic behaviour exactly: the position of a file's
		// extension in EXTENSIONS is the preference, and nothing else is.
		return cmp.Compare(c.extensionRank(a), c.extensionRank(b))
	}

	if r := cmp.Compare(bandRank(a, pref), bandRank(b, pref)); r != 0 {
		return r
	}

	// Within a band, better is always better -- for both settings.
	rateA, rateB := effectiveBitRate(a), effectiveBitRate(b)

	// A file nobody described is eligible but never preferred: we cannot say
	// it is the best of its band without knowing what it is.
	switch {
	case rateA == 0 && rateB == 0:
		return 0
	case rateA == 0:
		return 1
	case rateB == 0:
		return -1
	}

	return cmp.Compare(rateB, rateA)
}

// extensionRank is a file's position in the configured extension list.
// Anything unlisted sorts last, though the caller has already filtered those
// out.
func (c Slskd) extensionRank(file File) int {
	if i := slices.Index(c.Cfg.Filters.Extensions, file.Extension); i >= 0 {
		return i
	}
	return len(c.Cfg.Filters.Extensions)
}

// sortByPreference orders candidates in place. The sort is stable, so files
// that rank equally keep the order the search returned them in.
func (c Slskd) sortByPreference(files []File) {
	pref := normalisePreference(c.Cfg.SizePreference)
	slices.SortStableFunc(files, func(a, b File) int {
		return c.compareFiles(a, b, pref)
	})
}

// releaseBitRate summarises a release's quality as the median of its files'
// bitrates. The median rather than the mean, so that one 128 kbps bonus track
// cannot drag down an otherwise clean release. Files of unknown bitrate are
// left out; a release where nothing is known returns 0.
func releaseBitRate(files []File) int {
	rates := make([]int, 0, len(files))
	for _, file := range files {
		if rate := effectiveBitRate(file); rate > 0 {
			rates = append(rates, rate)
		}
	}

	if len(rates) == 0 {
		return 0
	}

	slices.Sort(rates)
	return rates[len(rates)/2]
}

// preferenceScore is what a release's quality contributes to its ranking.
//
// Deliberately smaller than either name-matching term in scoreDir: quality
// breaks a tie between releases that match the recommendation equally well and
// is never a reason to take a worse match. A wrong-album FLAC must still lose
// to a right-album MP3.
const preferenceScore = 20

// scorePreference rewards a release for sitting in the preferred band. It
// returns 0 for PreferNone, leaving release scoring exactly as it was.
func scorePreference(files []File, pref string) int {
	if pref == PreferNone {
		return 0
	}

	rate := releaseBitRate(files)
	if rate == 0 {
		return 0
	}

	// One file is enough to characterise the release: a directory of FLACs is
	// lossless whatever its median bitrate works out to.
	lossless := false
	for _, file := range files {
		if isLossless(file) {
			lossless = true
			break
		}
	}

	wantLossless := pref == PreferLarger
	if lossless == wantLossless {
		return preferenceScore
	}
	return 0
}
