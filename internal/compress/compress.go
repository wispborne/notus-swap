// Package compress decides when an answer may be sent gzipped.
package compress

import (
	"net/http"
	"strconv"
	"strings"
)

// AcceptsGzip reads the request's Accept-Encoding, where "gzip;q=0" means no.
func AcceptsGzip(r *http.Request) bool {
	for part := range strings.SplitSeq(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		q, ok := strings.CutPrefix(strings.TrimSpace(params), "q=")
		if v, err := strconv.ParseFloat(q, 64); ok && err == nil && v == 0 {
			return false
		}
		return true
	}
	return false
}
