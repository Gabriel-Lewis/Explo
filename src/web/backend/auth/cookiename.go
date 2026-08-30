package auth

import (
	"net"
	"strings"
)

// cookieNamePrefix is shared by every instance; what follows distinguishes them.
const cookieNamePrefix = "explo_session"

// DefaultCookieName derives a session cookie name from the address the server
// listens on.
//
// Cookies are scoped by host, not by port (RFC 6265 section 8.5), so several
// Explo instances on one machine -- the usual unraid setup, one container per
// port -- would otherwise all write a cookie called "session" for that host and
// overwrite each other. Sessions live in each process's own memory, so an ID
// minted by one instance is meaningless to the next, and the user is silently
// logged out of whichever instance they visited first.
//
// Including the port gives each instance its own cookie. Deployments that
// cannot be told apart this way (several instances behind one reverse proxy on
// the same host and port) should set UI_COOKIE_NAME explicitly.
func DefaultCookieName(addr string) string {
	port := portFromAddr(addr)
	if port == "" {
		return cookieNamePrefix
	}

	return cookieNamePrefix + "_" + port
}

// portFromAddr pulls the port out of a listen address. It accepts the forms
// Go's net package does -- ":7288", "0.0.0.0:7288", "[::]:7288" -- and returns
// "" for anything it cannot read, so a malformed WEB_ADDR degrades to the
// shared name rather than producing a nonsense cookie.
func portFromAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}

	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}

	return sanitizeCookieName(port)
}

// sanitizeCookieName keeps the name a valid cookie token. A port is normally
// digits, but WEB_ADDR may name a service instead ("http"), so anything outside
// the safe set is dropped rather than trusted into a Set-Cookie header.
func sanitizeCookieName(s string) string {
	var b strings.Builder

	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}

	return b.String()
}
