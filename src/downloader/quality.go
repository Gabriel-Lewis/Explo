package downloader

import "strings"

// losslessExtensions are the formats that carry no lossy compression. The
// split matters because it is the line people actually think in when they talk
// about a "small" or a "big" file, far more than any particular bitrate.
var losslessExtensions = map[string]struct{}{
	"flac": {},
	"wav":  {},
	"aiff": {},
	"aif":  {},
	"alac": {},
	"ape":  {},
	"wv":   {},
}

// isLossless reports whether a file's format is lossless.
func isLossless(file File) bool {
	_, ok := losslessExtensions[strings.ToLower(strings.TrimPrefix(file.Extension, "."))]
	return ok
}

// effectiveBitRate reports a file's bitrate in kbps.
//
// Soulseek peers often report no bitrate at all, which is why the existing
// quality floors are written to skip a file rather than judge it when BitRate
// is 0. A ceiling cannot be written that way round: skipping the files nobody
// described would let through exactly the ones a ceiling exists to exclude. So
// when the peer says nothing, derive the bitrate from how big the file is and
// how long it plays.
//
// The reported value is preferred where there is one, so files that already
// describe themselves are judged exactly as they were before.
//
// Returns 0 when neither is available, which callers must read as "unknown"
// rather than "zero": an unknown file is never rejected by the ceiling.
func effectiveBitRate(file File) int {
	if file.BitRate > 0 {
		return file.BitRate
	}

	if file.Size > 0 && file.Length > 0 {
		return file.Size * 8 / file.Length / 1000
	}

	return 0
}

// withinQualityRange reports whether a file sits inside the configured floor
// and ceiling. A file of unknown bitrate passes: it may well be fine, and
// dropping everything undescribed would throw away most of what Soulseek
// offers.
func (c Slskd) withinQualityRange(file File) bool {
	if file.BitDepth > 0 && file.BitDepth < c.Cfg.Filters.MinBitDepth {
		return false
	}

	bitRate := effectiveBitRate(file)
	if bitRate == 0 {
		return true
	}

	if c.Cfg.Filters.MinBitRate > 0 && bitRate < c.Cfg.Filters.MinBitRate {
		return false
	}

	// Zero disables the ceiling, so an upgrade changes nothing until someone
	// sets one.
	if c.Cfg.Filters.MaxBitRate > 0 && bitRate > c.Cfg.Filters.MaxBitRate {
		return false
	}

	return true
}
