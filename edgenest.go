package main

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
)

type EdgeNest struct {
	proxy *httputil.ReverseProxy
}

func NewEdgeNest(upstreamBase string) (*EdgeNest, error) {
	u, err := url.Parse(upstreamBase)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, errors.New("upstream URL missing scheme or host")
	}

	// Handles things for us like dropping hop-by-hop headers, making
	// streaming efficient (useful for blobs later).
	rp := httputil.NewSingleHostReverseProxy(u)

	// Digests depend on content's exact bytes.
	rp.Transport = &http.Transport{
		DisableCompression: true,
	}

	return &EdgeNest{
		proxy: rp,
	}, nil
}

var manifestPathRegex = regexp.MustCompile(`^/v2/(.+)/manifests/([^/]+)$`)

func (e *EdgeNest) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case manifestPathRegex.MatchString(r.URL.Path):
			e.handleManifest(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func (e *EdgeNest) handleManifest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		// Cache hit - get from our cache.
		//TODO - implement.

		// Cache miss - proxy upstream request/response.
		e.proxy.ServeHTTP(w, r)
	default:
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
