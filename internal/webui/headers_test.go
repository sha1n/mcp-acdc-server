package webui_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMount_PageResponsesRevalidateAndForbidDataImages(t *testing.T) {
	h := newHandler(t, &fakeBackend{}, newFakeReader())

	for _, target := range []string{"/ui/", "/ui/doc", "/ui/style.css"} {
		t.Run(target, func(t *testing.T) {
			rec := serve(h, http.MethodGet, target)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t,
				"default-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
				rec.Header().Get("Content-Security-Policy"))
			assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
		})
	}
}

func TestMount_APIResponsesAreNeverStored(t *testing.T) {
	h := newHandler(t, &fakeBackend{}, newFakeReader())

	for _, target := range []string{"/api/v1/config", "/api/v1/unknown"} {
		t.Run(target, func(t *testing.T) {
			rec := serve(h, http.MethodGet, target)

			assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
			assert.Empty(t, rec.Header().Get("Content-Security-Policy"))
		})
	}
}
