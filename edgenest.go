package main

import (
	"io"
	"net/http"
	"strings"
)

type EdgeNest struct {
}

func NewEdgeNest() EdgeNest {
	return EdgeNest{}
}

func (e EdgeNest) GetManifest(path string) *http.Response {
	header := http.Header{}
	header.Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
	header.Set("Docker-Content-Digest", "sha256:c0ffee")
	body := `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`
	return &http.Response{
		Status: "",
		Header: header,
		Body:   io.NopCloser(strings.NewReader(body)),
	}
}
