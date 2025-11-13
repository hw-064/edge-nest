package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// EdgeNest acts as a pull-only mirror for OCI container images,
// sitting between the compute node needing the image and the
// upstream container registry. It must adhere to the "Pull" category of the
// OCI Distribution specification: https://github.com/opencontainers/distribution-spec/tree/main
//
// Behaviour:
//   - Given a GET /v2/<name>/manifests/<reference> request, EdgeNest
//     must proxy it to the upstream registry /v2/... and return the same body
//     and key headers (Content-Type and Docker-Content-Digest) to the client.
func TestOCIPullOnlyMirror_Contract(t *testing.T) {
	t.Run("Manifest GET request proxies the upstream when fetching manifest from registry", func(t *testing.T) {
		// Arrange
		const manifestBody = `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`
		const manifestDigest = "sha256:c0ffee"
		const manifestPath = "/v2/library/alpine/manifests/latest"

		// Simulate our OCI upstream registry.
		getManifestHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// We should preserve the path when contacting upstream.
			if r.URL.Path != manifestPath {
				t.Fatalf("path not matching original requested path: %q", r.URL.Path)
			}

			w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, manifestBody)
		})

		upstream := httptest.NewServer(getManifestHandler)
		defer upstream.Close()

		e, err := NewEdgeNest(upstream.URL)
		if err != nil {
			t.Fatalf("Failed to create EdgeNest instance = %v", err)
		}

		mux := http.NewServeMux()
		e.RegisterRoutes(mux)

		// Act
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, manifestPath, nil)
		mux.ServeHTTP(rec, req)

		got := rec.Result()
		defer got.Body.Close()

		// Assert: compare with the upstream handler's own response.
		upstreamRecorder := httptest.NewRecorder()
		upstreamReq := httptest.NewRequest(http.MethodGet, manifestPath, nil)
		getManifestHandler.ServeHTTP(upstreamRecorder, upstreamReq)

		want := upstreamRecorder.Result()
		defer want.Body.Close()

		if gotCT := got.Header.Get("Content-Type"); gotCT != want.Header.Get("Content-Type") {
			t.Fatalf("unexpected Content-Type: got %q, want %q", gotCT, want.Header.Get("Content-Type"))
		}

		if gotDigest := got.Header.Get("Docker-Content-Digest"); gotDigest != want.Header.Get("Docker-Content-Digest") {
			t.Fatalf("unexpected Docker-Content-Digest: got %q, want %q", gotDigest, want.Header.Get("Docker-Content-Digest"))
		}

		wantBody, err := io.ReadAll(want.Body)
		if err != nil {
			t.Fatalf("reading wanted body: %v", err)
		}
		gotBody, err := io.ReadAll(got.Body)
		if err != nil {
			t.Fatalf("reading got body: %v", err)
		}

		if strings.TrimSpace(string(gotBody)) != strings.TrimSpace(string(wantBody)) {
			t.Fatalf("unexpected body: got %q, want %q", string(gotBody), string(wantBody))
		}
	})

	t.Run("Manifest GET request with invalid manifest path errors out", func(t *testing.T) {
		// Cases: empty string, not absolute path, etc.

	})

	t.Run("Manifest GET request always contains header Docker-Content-Digest (cache hit)", func(t *testing.T) {
		// Say we have cache hit and EdgeNest doesn't need to contact upstream.
		// We should make sure we always have Docker-Content-Digest header set.

	})
}
