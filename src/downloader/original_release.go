package downloader

import (
	"log/slog"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"explo/src/models"
)

// Preferring the original release is a trim, not a rejection. A peer sharing a
// six-disc anthology usually has the album we want sitting inside it, so
// reducing the candidate to that album beats skipping the peer and beats
// downloading all six discs.
//
// The trim runs before scoring, which is the point: a 39-file box set and a
// 16-file pressing of the same album are not comparable until both have been
// reduced to the album, and fullerReleaseScore would otherwise hand the box set
// the win on bulk alone.

// discTrackPattern matches a flattened "disc-track" prefix such as 01-04 or
// 1-09. The leading boundary rejects a digit, so a year like 1975-04 cannot be
// read as disc 19 or disc 5.
var discTrackPattern = regexp.MustCompile(`(?:^|[^0-9])([0-9]{1,2})-([0-9]{2})(?:[^0-9]|$)`)

// packedDiscTrackPattern matches the four-digit DDTT form, as in "0101 - Colors".
// The disc half is capped at 09 so that a leading year (1999, 2015) is not read
// as disc 19 or disc 20.
var packedDiscTrackPattern = regexp.MustCompile(`^(0[1-9])([0-9]{2})(?:[^0-9]|$)`)

// leadingTrackPattern matches a plain track number: "02 - Sign of the Times".
var leadingTrackPattern = regexp.MustCompile(`^([0-9]{1,2})(?:[^0-9]|$)`)

// underscoreTrackPattern matches the track number in an underscore-joined name,
// as in "Hayley Williams_Ego Death_18_Parachute". Limited to one or two digits
// so an embedded year cannot match, and tried after the disc-aware patterns so
// that "..._01-04_Chocolate" is read as disc 1 rather than as track 1.
var underscoreTrackPattern = regexp.MustCompile(`_([0-9]{1,2})_`)

// yearPattern finds a release year in a directory name.
var yearPattern = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)[0-9]{2})(?:[^0-9]|$)`)

// originalYearPenalty is how much a directory loses for naming a year later
// than the album's original release. Deliberately small: it only separates
// releases that already survived the trim as equals, which is the "then
// earliest" half of preferring the original.
const originalYearPenalty = 8

// fileNumbering is what a peer's filename says about where the file sits in the
// release. disc is 0 when the name carries no disc marker at all, which is the
// normal case for a single-disc directory.
type fileNumbering struct {
	disc  int
	track int
	ok    bool
}

// parseNumbering reads the disc and track position out of a filename. Peers
// name files every way imaginable, so this recognises the three forms that
// actually show up and gives up on anything else rather than guessing.
func parseNumbering(name string) fileNumbering {
	base := path.Base(normalizePeerPath(name))
	base = strings.TrimSuffix(base, path.Ext(base))

	if m := packedDiscTrackPattern.FindStringSubmatch(base); m != nil {
		disc, _ := strconv.Atoi(m[1])
		trackNo, _ := strconv.Atoi(m[2])
		if trackNo > 0 {
			return fileNumbering{disc: disc, track: trackNo, ok: true}
		}
	}

	if m := discTrackPattern.FindStringSubmatch(base); m != nil {
		disc, _ := strconv.Atoi(m[1])
		trackNo, _ := strconv.Atoi(m[2])
		if disc > 0 && trackNo > 0 {
			return fileNumbering{disc: disc, track: trackNo, ok: true}
		}
	}

	if m := leadingTrackPattern.FindStringSubmatch(base); m != nil {
		trackNo, _ := strconv.Atoi(m[1])
		if trackNo > 0 {
			return fileNumbering{track: trackNo, ok: true}
		}
	}

	if m := underscoreTrackPattern.FindStringSubmatch(base); m != nil {
		trackNo, _ := strconv.Atoi(m[1])
		if trackNo > 0 {
			return fileNumbering{track: trackNo, ok: true}
		}
	}

	return fileNumbering{}
}

// minParsedShare is how much of a directory must yield a usable track number
// before the trim will act on it. Below this the directory is named in some way
// we do not understand, and trimming would be cutting at random -- a peer
// sharing a literal "{track-number:02d} - Cowgirl.mp3" is not hypothetical.
const minParsedShare = 2.0 / 3.0

// allowedDiscs is how many discs the release may legitimately span.
//
// MusicBrainz is trusted when it has an answer: a real double album keeps both
// discs. With no answer the assumption is one disc, because the case this
// exists for -- a peer flattening several discs into a single folder -- is far
// more common than an unenriched genuine double album.
func allowedDiscs(track models.Track) int {
	if track.DiscTotal > 1 {
		return track.DiscTotal
	}
	return 1
}

// trimToOriginal reduces a candidate release to the original album: extra discs
// first, then bonus tracks appended past the album's real length.
//
// The primary is retained unconditionally. Trimming away the recommended track
// would lose the playlist entry entirely, which is a worse outcome than
// downloading one file more than intended -- and a recommendation that is
// itself a bonus track is exactly the case that would otherwise do it.
func trimToOriginal(dir *peerDir, track models.Track) {
	if dir.primary == nil || len(dir.files) < 2 {
		return
	}

	numbering := make([]fileNumbering, len(dir.files))
	var parsed int
	for i, file := range dir.files {
		numbering[i] = parseNumbering(string(file.Name))
		if numbering[i].ok {
			parsed++
		}
	}
	if float64(parsed) < float64(len(dir.files))*minParsedShare {
		return
	}

	primaryName := dir.primary.Name
	keep := make([]bool, len(dir.files))
	for i := range keep {
		keep[i] = true
	}

	discs := trimExtraDiscs(dir, numbering, keep, track)

	// The bonus-track pass is skipped whenever more than one disc survives:
	// TrackTotal counts the first medium only, so measuring a kept two-disc
	// release against it would amputate the second disc for being there.
	if discs <= 1 {
		trimBonusTracks(dir, numbering, keep, track)
	}

	kept := make([]File, 0, len(dir.files))
	for i, file := range dir.files {
		if keep[i] || file.Name == primaryName {
			kept = append(kept, file)
		}
	}
	if len(kept) == len(dir.files) {
		return
	}

	slog.Debug("trimmed release to the original album",
		"dir", dir.dir, "from", len(dir.files), "to", len(kept))

	dir.files = kept
	// dir.primary points into the old backing array, so it has to be re-seated
	// or every later use of it reads a file that is no longer in the release.
	dir.primary = nil
	for i := range dir.files {
		if dir.files[i].Name == primaryName {
			dir.primary = &dir.files[i]
			break
		}
	}
}

// trimExtraDiscs drops discs beyond what the release may legitimately span,
// keeping the lowest-numbered ones. It reports how many discs survive.
func trimExtraDiscs(dir *peerDir, numbering []fileNumbering, keep []bool, track models.Track) int {
	present := make([]int, 0, 4)
	for _, n := range numbering {
		if n.ok && n.disc > 0 && !slices.Contains(present, n.disc) {
			present = append(present, n.disc)
		}
	}
	if len(present) <= 1 {
		return len(present)
	}

	slices.Sort(present)
	allowed := allowedDiscs(track)
	if len(present) <= allowed {
		return len(present)
	}

	wanted := present[:allowed]
	for i, n := range numbering {
		if n.ok && n.disc > 0 && !slices.Contains(wanted, n.disc) {
			keep[i] = false
		}
	}
	return allowed
}

// trimBonusTracks drops whatever sits past the album's real track count.
//
// Bonus material is appended after the album by near-universal convention, so
// the lowest TrackTotal positions are the album. Files with no readable track
// number sort last and are therefore the first to go.
func trimBonusTracks(dir *peerDir, numbering []fileNumbering, keep []bool, track models.Track) {
	expected := track.TrackTotal
	if expected <= 0 {
		return
	}

	order := make([]int, 0, len(dir.files))
	for i := range dir.files {
		if keep[i] {
			order = append(order, i)
		}
	}
	if len(order) <= expected {
		return
	}

	slices.SortStableFunc(order, func(a, b int) int {
		na, nb := numbering[a], numbering[b]
		switch {
		case na.ok != nb.ok:
			if na.ok {
				return -1
			}
			return 1
		case na.track != nb.track:
			return na.track - nb.track
		default:
			return 0
		}
	})

	for _, i := range order[expected:] {
		keep[i] = false
	}
}

// laterThanOriginal reports whether a directory names a release year after the
// album's original one -- a remaster or reissue carrying the same tracklist,
// which the trim cannot tell apart from the original by file count alone.
//
// Only the final path segment is read: the parent folders belong to the peer's
// library layout, and a year in one of those says nothing about this release.
func laterThanOriginal(dir string, track models.Track) bool {
	if track.OriginalYear <= 0 {
		return false
	}

	matches := yearPattern.FindAllStringSubmatch(path.Base(normalizePeerPath(dir)), -1)
	if len(matches) == 0 {
		return false
	}

	earliest := 0
	for _, m := range matches {
		year, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if earliest == 0 || year < earliest {
			earliest = year
		}
	}

	return earliest > track.OriginalYear
}
