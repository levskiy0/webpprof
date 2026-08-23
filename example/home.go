package main

import (
	_ "embed"
	"net/http"
)

//go:embed home.html
var homePage string

func (a *application) home(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(homePage))
}
