package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func setupEdgeNestHandler(t *testing.T, upstreamHandler http.Handler) *http.ServeMux {
	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)

	e, err := NewEdgeNest(upstream.URL)
	if err != nil {
		t.Fatalf("Failed to create EdgeNest: %v", err)
	}

	mux := http.NewServeMux()
	e.RegisterRoutes(mux)

	return mux
}

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

		edgeNestHandler := setupEdgeNestHandler(t, getManifestHandler)

		// Act
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, manifestPath, nil)
		edgeNestHandler.ServeHTTP(rec, req)

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

	t.Run("Manifest GET request always contains header Docker-Content-Digest (cache hit)", func(t *testing.T) {
		// Say we have cache hit and EdgeNest doesn't need to contact upstream.
		// We should make sure we always have Docker-Content-Digest header set.

	})
}

func TestManifestHEAD(t *testing.T) {
	t.Run("HEAD request returns headers without body", func(t *testing.T) {
		// Arrange
		const manifestDigest = "sha256:c0ffee"
		const manifestPath = "/v2/library/alpine/manifests/latest"

		upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodHead {
				t.Errorf("upstream received %s, expected HEAD", r.Method)
			}

			w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			w.WriteHeader(http.StatusOK)
		})

		mux := setupEdgeNestHandler(t, upstreamHandler)

		// Act
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodHead, manifestPath, nil)
		mux.ServeHTTP(rec, req)

		got := rec.Result()
		defer got.Body.Close()

		// Assert: compare with the upstream handler's own response
		upstreamRec := httptest.NewRecorder()
		upstreamReq := httptest.NewRequest(http.MethodHead, manifestPath, nil)
		upstreamHandler.ServeHTTP(upstreamRec, upstreamReq)

		want := upstreamRec.Result()
		defer want.Body.Close()

		if got.Header.Get("Content-Type") != want.Header.Get("Content-Type") {
			t.Errorf("Content-Type = %q, want %q", got.Header.Get("Content-Type"), want.Header.Get("Content-Type"))
		}

		if got.Header.Get("Docker-Content-Digest") != want.Header.Get("Docker-Content-Digest") {
			t.Errorf("Docker-Content-Digest = %q, want %q", got.Header.Get("Docker-Content-Digest"), want.Header.Get("Docker-Content-Digest"))
		}

		gotBody, _ := io.ReadAll(got.Body)
		if len(gotBody) != 0 {
			t.Errorf("body = %q, want empty", string(gotBody))
		}
	})
}

func TestManifestPathValidation(t *testing.T) {
	// HEAD goes through same path so we just test with GET here to be pragmatic.
	// If we want to be comprehensive we could table in the HEAD method.
	t.Run("Manifest GET request with invalid manifest path errors out", func(t *testing.T) {
		cases := []struct {
			name string
			path string
		}{
			{
				name: "Path only has v2 prefix",
				path: "/v2/",
			},
			{
				name: "missing name.",
				path: "/v2/manifests/latest",
			},
			{
				name: "missing reference",
				path: "/v2/library/alpine/manifests/",
			},
			{
				name: "missing 'manifests' segment",
				path: "/v2/library/alpine/latest",
			},
			{
				name: "extra trailing slashes",
				path: "/v2/library/alpine/manifests/latest/",
			},
		}

		upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("upstream should not be called for invalid paths")
		})

		mux := setupEdgeNestHandler(t, upstreamHandler)

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, tc.path, nil)
				mux.ServeHTTP(rec, req)

				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
				}
			})
		}
	})
}
