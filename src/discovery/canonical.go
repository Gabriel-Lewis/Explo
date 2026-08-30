package discovery

import "strings"

// The album's shape as its release group agrees it is.
//
// A recommendation carries whichever release MusicBrainz matched, which is not
// necessarily the album anyone thinks of as the album: a track resolving to a
// two-disc deluxe edition reports two discs and the deluxe's length, and album
// mode then treats a padded directory as correct. Asking the rest of the group
// what shape the album usually has answers that far more reliably than trusting
// the single matched release.
//
// This costs nothing extra. The enrichment request already asks for
// inc=media+releases and parses every release; until now all but one were
// discarded.

// inflatedFormats report more media than the album has discs. A 2LP pressing of
// a single-CD album lists two media, which would drag the disc consensus up.
var inflatedFormats = []string{"vinyl", "cassette"}

func inflatesDiscCount(rel MBRelease) bool {
	for _, medium := range rel.Media {
		for _, format := range inflatedFormats {
			if strings.EqualFold(medium.Format, format) {
				return true
			}
		}
	}
	return false
}

// CanonicalReleaseShape reports the modal first-medium track count and the modal
// medium count across the releases sharing groupID, or zeroes when there is no
// usable consensus. Zero leaves every caller on the matched release's own
// numbers, which is what happens with enrichment switched off.
func CanonicalReleaseShape(releases []MBRelease, groupID string) (trackTotal, discTotal int) {
	if groupID == "" {
		return 0, 0
	}

	group := make([]MBRelease, 0, len(releases))
	for _, rel := range releases {
		// Other release groups carry this recording too -- greatest hits,
		// soundtracks -- and their lengths say nothing about this album.
		if rel.ReleaseGroup.ID != groupID || len(rel.Media) == 0 || inflatesDiscCount(rel) {
			continue
		}
		group = append(group, rel)
	}
	if len(group) == 0 {
		return 0, 0
	}

	// Bootlegs and promos are poor evidence of the album's real shape, but they
	// are better than nothing: a group with no official release at all still
	// gets a consensus rather than falling back to the matched release.
	official := make([]MBRelease, 0, len(group))
	for _, rel := range group {
		if strings.EqualFold(rel.Status, "Official") {
			official = append(official, rel)
		}
	}
	if len(official) > 0 {
		group = official
	}

	tracks := make([]int, 0, len(group))
	discs := make([]int, 0, len(group))
	for _, rel := range group {
		if count := rel.Media[0].TrackCount; count > 0 {
			tracks = append(tracks, count)
		}
		discs = append(discs, len(rel.Media))
	}

	return modal(tracks), modal(discs)
}

// modal returns the most common value, breaking ties toward the smaller one.
//
// The tie-break is the point of the whole exercise: when an album is shared
// equally as a 12-track original and a 20-track expanded edition, the original
// is the one being asked for.
func modal(values []int) int {
	counts := make(map[int]int, len(values))
	for _, value := range values {
		counts[value]++
	}

	var best, bestCount int
	for value, count := range counts {
		if count > bestCount || (count == bestCount && value < best) {
			best, bestCount = value, count
		}
	}
	return best
}
