package models

// for structs used across the project

type Track struct {
	Album                     string
	AlbumID                   string  // Used by Lidarr
	AlbumArtist               string
	ID                        string
	Artist                    string // All artists as returned by LB
	Artists                   []string
	MainArtist                string
	MainArtistID              string
	ArtistSort                string
	ReleaseCountry            string
	ReleaseStatus             string
	ReleaseType               string
	CleanTitle                string // Title as returned by LB
	Title                     string // Title as built in listenbrainz.go
	File                      string // File name
	AlbumFiles                []string // Sibling files queued with File when album mode is on
	Size                      int    // File size
	Present                   bool   // is track present in the system or not
	Duration                  int    // Track duration in milliseconds (not available for every track)
	CoverURL                  string // External cover art URL (Cover Art Archive), used at run-time to download art
	CoverPath                 string // full Filesystem path to cover
	OriginalDate              string
	OriginalYear              int
	Genres                    string
	ISRCs                     []string
	Media                     string // Media format (e.g., CD, Digital Media)
	TrackNumber               int    // Track position in media
	TrackTotal                int    // Total tracks in media
	DiscNumber                int    // Disc/media position
	DiscTotal                 int    // Total discs/media
	// Canonical* describe the album as its release group agrees it is, rather
	// than as the one matched release happens to be. A recommendation resolving
	// to a deluxe edition still reports the standard album's shape here, which
	// is what album mode needs to recognise a padded release.
	CanonicalTrackTotal       int // Modal first-medium track count across the release group
	CanonicalDiscTotal        int // Modal medium count across the release group
	MusicBrainzReleaseGroupID string
	MusicBrainzAlbumArtistID  string
	MusicBrainzTrackID        string
	MusicBrainzAlbumID        string
	MusicBrainzReleaseTrackID string
	MusicBrainzArtistID       string
}
