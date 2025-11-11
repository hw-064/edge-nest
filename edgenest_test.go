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
	//TODO - consider using http.Client + httptest.NewServer instead of recorder to
	// simulate more realistic behaviour.
	t.Run("Manifest GET request proxies the upstream when fetching manifest from registry", func(t *testing.T) {
		// Arrange
		const manifestBody = `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`
		const manifestDigest = "sha256:c0ffee"
		const manifestPath = "/v2/library/alpine/manifests/latest"

		// Simulate our OCI upstream registry.
		getManifestHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// We should preserve the path when contacting upstream.
			if r.URL.Path != manifestPath {
				t.Fatalf("Path not matching original requested path: %q", r.URL.Path)
			}

			w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, manifestBody)
		})

		// Act
		e := NewEdgeNest()
		res := e.GetManifest(manifestPath)
		defer res.Body.Close()

		// Assert
		upstreamRecorder := httptest.NewRecorder()
		upstreamReq := httptest.NewRequest(http.MethodGet, "/v2/library/alpine/manifests/latest", nil)
		getManifestHandler.ServeHTTP(upstreamRecorder, upstreamReq)

		want := upstreamRecorder.Result()
		defer want.Body.Close()

		if gotCT := res.Header.Get("Content-Type"); gotCT != want.Header.Get("Content-Type") {
			t.Fatalf("unexpected Content-Type: %q", gotCT)
		}

		if gotDigest := res.Header.Get("Docker-Content-Digest"); gotDigest != want.Header.Get("Docker-Content-Digest") {
			t.Fatalf("unexpected Docker-Content-Digest: %q", gotDigest)
		}

		wantBody, err := io.ReadAll(want.Body)
		if err != nil {
			t.Fatalf("reading wanted body: %v", err)
		}
		gotBody, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("reading got body: %v", err)
		}

		if strings.TrimSpace(string(gotBody)) != strings.TrimSpace(string(wantBody)) {
			t.Fatalf("unexpected body: %q", string(gotBody))
		}

	})

	t.Run("Manifest GET request always contains header Docker-Content-Digest (cache hit)", func(t *testing.T) {
		// Say we have cache hit and EdgeNest doesn't need to contact upstream.
		// We should make sure we always have Docker-Content-Digest header set.

	})
}
