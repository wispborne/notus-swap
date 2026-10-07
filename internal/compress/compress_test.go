package compress

import (
	"net/http"
	"testing"
)

func TestAcceptsGzip(t *testing.T) {
	for header, want := range map[string]bool{
		"":                     false,
		"gzip":                 true,
		"gzip, deflate, br":    true,
		"br, GZIP":             true,
		"deflate":              false,
		"gzip;q=0":             false,
		"gzip; q=0.0, deflate": false,
		"gzip;q=0.5":           true,
		"x-gzip":               false,
	} {
		r, _ := http.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Encoding", header)
		if got := AcceptsGzip(r); got != want {
			t.Errorf("%q: got %v, want %v", header, got, want)
		}
	}
}
