package main

import (
	"net/http"
)

type EdgeNest struct {
}

func NewEdgeNest() EdgeNest {
	return EdgeNest{}
}

func (e EdgeNest) GetManifest(path string) *http.Response {
	return &http.Response{}
}
