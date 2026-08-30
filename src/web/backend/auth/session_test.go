package auth

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"
)

// instance stands in for one Explo container: its own session manager, its own
// in-memory store, and its own listening port.
type instance struct {
	manager *SessionManager
	server  *httptest.Server
}

func newInstance(t *testing.T, cookieName string) *instance {
	t.Helper()

	manager := NewSessionManager(NewInMemorySessionStore(), time.Hour, 7*24*time.Hour, cookieName)

	handler := manager.Handle(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := manager.GetSession(r)
		if r.URL.Path == "/login" {
			session.Put("authenticated", true)
			w.WriteHeader(http.StatusOK)
			return
		}
		if authenticated, _ := session.Get("authenticated").(bool); !authenticated {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return &instance{manager: manager, server: server}
}

// closeBody releases a response body, reporting rather than swallowing a
// failure to do so.
func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()

	if err := resp.Body.Close(); err != nil {
		t.Errorf("closing response body: %v", err)
	}
}

// get drives one request from the shared browser jar.
func get(t *testing.T, client *http.Client, url string) int {
	t.Helper()

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer closeBody(t, resp)

	return resp.StatusCode
}

// Two Explo instances on one unraid host are two ports on the same hostname.
// Cookies are not scoped by port (RFC 6265 section 8.5), so a fixed cookie
// name means the second instance overwrites the first's session ID, and
// neither store recognises the other's, logging the user out.
func TestSessionCookie_InstancesDoNotClobberEachOther(t *testing.T) {
	first := newInstance(t, DefaultCookieName(":7288"))
	second := newInstance(t, DefaultCookieName(":7289"))

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("creating cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	if code := get(t, client, first.server.URL+"/login"); code != http.StatusOK {
		t.Fatalf("logging into the first instance = %d, want 200", code)
	}

	// Merely visiting the second instance must not disturb the first.
	if code := get(t, client, second.server.URL+"/"); code != http.StatusUnauthorized {
		t.Fatalf("second instance = %d, want 401 -- it shares no session with the first", code)
	}

	if code := get(t, client, first.server.URL+"/"); code != http.StatusOK {
		t.Errorf("first instance = %d, want 200 -- the second instance clobbered its session cookie", code)
	}
}

// The whole point of the default: two addresses must not produce one name.
func TestDefaultCookieName_DiffersPerAddress(t *testing.T) {
	if a, b := DefaultCookieName(":7288"), DefaultCookieName(":7289"); a == b {
		t.Errorf("both addresses produced %q; instances on one host would share a cookie", a)
	}
}

// WEB_ADDR is free-form, so the derivation has to cope with every listen
// address Go accepts, and degrade rather than emit a broken cookie name.
func TestDefaultCookieName_AddressForms(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{"port only", ":7288", "explo_session_7288"},
		{"host and port", "0.0.0.0:7288", "explo_session_7288"},
		{"ipv6", "[::]:7288", "explo_session_7288"},
		{"named service", ":http", "explo_session_http"},
		{"no port falls back to the shared name", "0.0.0.0", "explo_session"},
		{"empty falls back to the shared name", "", "explo_session"},
		{"unsafe characters are dropped", ":72 88;x", "explo_session_7288x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DefaultCookieName(tt.addr); got != tt.want {
				t.Errorf("DefaultCookieName(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

// A derived name is only useful if the session actually round-trips under it.
func TestSessionCookie_UsesTheConfiguredName(t *testing.T) {
	const name = "explo_session_7288"

	instance := newInstance(t, name)

	resp, err := http.Get(instance.server.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	defer closeBody(t, resp)

	for _, cookie := range resp.Cookies() {
		if cookie.Name == name {
			return
		}
	}
	t.Errorf("no %q cookie in %v", name, resp.Cookies())
}
