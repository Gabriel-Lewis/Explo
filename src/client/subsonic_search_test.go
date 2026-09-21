package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"explo/src/models"
	"explo/src/util"
)

// queryRecordingServer reports the raw query string of every request.
func queryRecordingServer(t *testing.T, queries *[]string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*queries = append(*queries, r.URL.Query().Get("query"))
		if _, err := w.Write([]byte(`{}`)); err != nil {
			t.Errorf("fake server failed to write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	return server
}

// search3 returns a bounded result set, so a title on its own can fill it with
// recordings by other artists and never include the one being looked for.
func TestSubsonicSearchSongs_QueriesByArtistAsWell(t *testing.T) {
	var queries []string
	server := queryRecordingServer(t, &queries)

	sub := NewSubsonic(testConfig(server.URL, ""), util.NewHttp(util.HttpClientConfig{Timeout: 5}))

	tracks := []*models.Track{{CleanTitle: "hurt", MainArtist: "Nine Inch Nails"}}
	if err := sub.SearchSongs(tracks); err != nil {
		t.Fatalf("SearchSongs() = %v, want nil", err)
	}

	if len(queries) == 0 {
		t.Fatal("no search request was made")
	}
	if queries[0] != "hurt Nine Inch Nails" {
		t.Errorf("query = %q, want %q", queries[0], "hurt Nine Inch Nails")
	}
}

// A track with no artist must not send a trailing separator.
func TestSubsonicSearchSongs_OmitsAnAbsentArtist(t *testing.T) {
	var queries []string
	server := queryRecordingServer(t, &queries)

	sub := NewSubsonic(testConfig(server.URL, ""), util.NewHttp(util.HttpClientConfig{Timeout: 5}))

	tracks := []*models.Track{{CleanTitle: "hurt"}}
	if err := sub.SearchSongs(tracks); err != nil {
		t.Fatalf("SearchSongs() = %v, want nil", err)
	}

	if len(queries) == 0 {
		t.Fatal("no search request was made")
	}
	if queries[0] != "hurt" {
		t.Errorf("query = %q, want %q", queries[0], "hurt")
	}
}
