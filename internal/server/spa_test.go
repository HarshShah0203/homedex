package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestSPAFileServer(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":        {Data: []byte("<main>homedex</main>")},
		"assets/app-123.js": {Data: []byte("console.log('homedex')")},
	}
	handler := spaFileServer(assets)

	for _, test := range []struct {
		path string
		want int
		body string
	}{
		{path: "/assets/app-123.js", want: http.StatusOK, body: "console.log"},
		{path: "/hosts/42", want: http.StatusOK, body: "<main>homedex</main>"},
		{path: "/api/not-a-route", want: http.StatusNotFound, body: "404 page not found"},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.want || !contains(recorder.Body.String(), test.body) {
			t.Errorf("%s: status=%d body=%q", test.path, recorder.Code, recorder.Body.String())
		}
	}
}

func contains(value, fragment string) bool {
	for i := 0; i+len(fragment) <= len(value); i++ {
		if value[i:i+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
