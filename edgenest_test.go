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

func assertManifestResponsesMatch(t *testing.T, got, want *http.Response) {
	t.Helper()

	if got.Header.Get("Content-Type") != want.Header.Get("Content-Type") {
		t.Errorf("Content-Type = %q, want %q",
			got.Header.Get("Content-Type"), want.Header.Get("Content-Type"))
	}

	if got.Header.Get("Docker-Content-Digest") != want.Header.Get("Docker-Content-Digest") {
		t.Errorf("Docker-Content-Digest = %q, want %q",
			got.Header.Get("Docker-Content-Digest"), want.Header.Get("Docker-Content-Digest"))
	}

	wantBody, err := io.ReadAll(want.Body)
	if err != nil {
		t.Fatalf("failed to read want body: %v", err)
	}

	gotBody, err := io.ReadAll(got.Body)
	if err != nil {
		t.Fatalf("failed to read got body: %v", err)
	}

	if strings.TrimSpace(string(gotBody)) != strings.TrimSpace(string(wantBody)) {
		t.Fatalf("unexpected body: got %q, want %q", string(gotBody), string(wantBody))
	}
}

// EdgeNest acts as a pull-only mirror for OCI container images,
// sitting between the compute node needing the image and the
// upstream container registry. It must adhere to the "Pull" category of the
// OCI Distribution specification: https://github.com/opencontainers/distribution-spec/tree/main

// Behaviour:
//   - Given a GET /v2/<name>/manifests/<reference> request, EdgeNest
//     must proxy it to the upstream registry /v2/... and return the same body
//     and key headers (Content-Type and Docker-Content-Digest) to the client.
func TestManifestGET(t *testing.T) {
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

		assertManifestResponsesMatch(t, got, want)
	})

	t.Run("Manifest GET request always contains header Docker-Content-Digest (cache hit)", func(t *testing.T) {
		// Say we have cache hit and EdgeNest doesn't need to contact upstream.
		// We should make sure we always have Docker-Content-Digest header set.

	})
}

// Behaviour:
//   - Given a HEAD /v2/<name>/manifests/<reference> request, EdgeNest
//     must proxy it to the upstream registry /v2/... and return the same response with
//     key headers (Content-Type and Docker-Content-Digest) to the client.
//     No body should be returned.
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

// Behaviour:
//   - Manifest paths in GET or HEAD requests must be in valid format "/v2/<name>/manifests/<reference>", otherwise return 404.
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

// Behaviour:
//   - When invalid HTTP method requests are sent (non GET/HEAD), EdgeNest
//   - should return 405 status code and Include an Allow header listing the allowed methods
//   - in the response. EdgeNest should also never attempt to call upstream
//   - registry in the event of an invalid HTTP method request (for efficiency reasons).
func TestManifestMethodNotAllowed(t *testing.T) {
	t.Run("unsupported HTTP methods return 405", func(t *testing.T) {
		methods := []string{
			http.MethodPost,
			http.MethodPut,
			http.MethodDelete,
			http.MethodPatch,
			http.MethodOptions,
		}

		upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("upstream should not be called for unsupported methods")
		})

		mux := setupEdgeNestHandler(t, upstreamHandler)

		const manifestPath = "/v2/library/alpine/manifests/latest"

		for _, method := range methods {
			t.Run(method, func(t *testing.T) {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(method, manifestPath, nil)
				mux.ServeHTTP(rec, req)

				if rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
				}

				allowHeader := rec.Header().Get("Allow")
				if allowHeader != "GET, HEAD" {
					t.Errorf("Allow header = %q, want %q", allowHeader, "GET, HEAD")
				}
			})
		}
	})
}

// Behaviour:
//   - Context: EdgeNest proxies client's request to upstream registry.
//   - When a 404, 500, or 503 error response is returned from upstream registry,
//   - EdgeNest should simply proxy the response back to the client.
func TestManifestUpstreamErrorHandling(t *testing.T) {
	t.Run("proxies upstream error responses", func(t *testing.T) {
		cases := []struct {
			name       string
			statusCode int
			body       string
		}{
			{
				name:       "404 not found",
				statusCode: http.StatusNotFound,
				body:       `{"errors":[{"code":"MANIFEST_UNKNOWN","message":"manifest unknown"}]}`,
			},
			{
				name:       "500 internal server error",
				statusCode: http.StatusInternalServerError,
				body:       `{"errors":[{"code":"UNKNOWN","message":"internal error"}]}`,
			},
			{
				name:       "503 service unavailable",
				statusCode: http.StatusServiceUnavailable,
				body:       "service temporarily unavailable",
			},
		}

		const manifestPath = "/v2/library/alpine/manifests/latest"

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tc.statusCode)
					w.Write([]byte(tc.body))
				})

				mux := setupEdgeNestHandler(t, upstreamHandler)

				// We'll just test with GET to be pragmatic.
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, manifestPath, nil)
				mux.ServeHTTP(rec, req)

				if rec.Code != tc.statusCode {
					t.Errorf("status = %d, want %d", rec.Code, tc.statusCode)
				}

				gotBody := strings.TrimSpace(rec.Body.String())
				wantBody := strings.TrimSpace(tc.body)
				if gotBody != wantBody {
					t.Errorf("body = %q, want %q", gotBody, wantBody)
				}
			})
		}
	})
}

// Behaviour:
//   - When EdgeNest receives successive GET/HEAD requests for the same manifest
//     that have been received before, EdgeNest should return cached responses
//     including manifest body back to the client. For cache hits, EdgeNest should
//     not attempt to contact the upstream registry.
func TestManifestCaching(t *testing.T) {
	t.Run("first request fetches from upstream, second serves from cache", func(t *testing.T) {
		// Arrange
		const manifestBody = `{"schemaVersion":2}`
		const manifestDigest = "sha256:c0ffee"
		const manifestPath = "/v2/library/alpine/manifests/latest"

		upstreamCallCount := 0
		upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upstreamCallCount++
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(manifestBody))
		})

		cache := NewCache()
		mux := setupEdgeNestHandler(t, upstreamHandler, cache)

		// Our first request to get the manifest is a cache miss, so the upstream
		// should be called to proxy the response back.
		upstreamRec := httptest.NewRecorder()
		upstreamReq := httptest.NewRequest(http.MethodGet, manifestPath, nil)
		upstreamHandler.ServeHTTP(upstreamRec, upstreamReq)

		want := upstreamRec.Result()
		defer want.Body.Close()

		rec1 := httptest.NewRecorder()
		req1 := httptest.NewRequest(http.MethodGet, manifestPath, nil)
		mux.ServeHTTP(rec1, req1)
		got1 := rec1.Result()
		defer got1.Body.Close()

		assertManifestResponsesMatch(t, got1, want)
		// We check that count is zero and later that count doesn't change,
		// so that we can accomodate any retry logic with upstream without
		// breaking this test.
		if upstreamCallCount == 0 {
			t.Errorf("After first request, upstream should have been called but it wasn't.", upstreamCallCount)
		}
		upstreamCallCountAfterFirstRequest := upstreamCallCount

		// We'll request to get the same manifest, expecting a cache hit. Upstream
		// shouldn't be called.
		rec2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, manifestPath, nil)
		mux.ServeHTTP(rec2, req2)

		got2 := rec2.Result()
		defer got2.Body.Close()

		if upstreamCallCount > upstreamCallCountAfterFirstRequest {
			t.Errorf("after second request, upstream shouldn't be called. Upstream was called %d times, want $", upstreamCallCount, upstreamCallCountAfterFirstRequest)
		}
		assertManifestResponsesMatch(t, got2, want)

	})
}
