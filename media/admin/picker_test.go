// SPDX-License-Identifier: MPL-2.0
package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/media"
)

func TestPickerPageFiltersCurrentExactSelectionRights(t *testing.T) {
	first, second := sample(), sample()
	second.ID = "BBBBBBBBBBBBBBBBBBBBBBBBBB"
	second.Filename = "not-visible-to-picker.pdf"
	s := &libraryStub{page: media.Page{Assets: []media.Asset{first, second}, NextCursor: "paged-position"}}
	view := requestView()
	view.CheckAllowed = func(cap, target string) (bool, error) {
		return cap == media.Select && target == first.ID, nil
	}
	w := httptest.NewRecorder()
	handlerFor(s).serve(w, httptest.NewRequest(http.MethodGet, "/picker/page", nil), view)
	var result pickerPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil ||
		len(result.Assets) != 1 || result.Assets[0].ID != first.ID ||
		result.Assets[0].Revision != first.Revision || result.NextCursor != "paged-position" ||
		strings.Contains(w.Body.String(), second.Filename) || s.limit != 25 || s.actor != "operator" || strings.Join(s.calls, ",") != "list" {
		t.Fatal("picker disclosed unavailable assets or lost bounded service pagination", w.Code, w.Body.String(), s.calls)
	}
	view.CheckAllowed = func(_, _ string) (bool, error) { return false, achrix.ErrAuthorizationUnavailable }
	w = httptest.NewRecorder()
	handlerFor(s).serve(w, httptest.NewRequest(http.MethodGet, "/picker/page", nil), view)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "authorization_unavailable") || strings.Contains(w.Body.String(), first.Filename) {
		t.Fatal("unavailable per-asset policy must fail closed", w.Code, w.Body.String())
	}
}

func TestPickerExactSelectionRechecksReadyAndRights(t *testing.T) {
	s := &libraryStub{asset: sample()}
	view := requestView()
	view.Localizer = presentationLocalizer(t, "fa")
	target := "/picker/asset/" + testID
	w := httptest.NewRecorder()
	handlerFor(s).serve(w, httptest.NewRequest(http.MethodGet, target, nil), view)
	var result struct {
		Asset   asset  `json:"asset"`
		Message string `json:"message"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil ||
		s.target != testID || s.actor != "operator" || strings.Join(s.calls, ",") != "select" ||
		result.Asset.Revision != 2 || result.Asset.State != "ready" ||
		!strings.Contains(result.Message, s.asset.Filename) ||
		!strings.Contains(w.Body.String(), `"revision":"2"`) || strings.Contains(w.Body.String(), "sha256") || strings.Contains(w.Body.String(), "content") {
		t.Fatal("picker did not return a current typed reference without private bytes", w.Code, w.Body.String())
	}
	s.asset.State = "deleted"
	w = httptest.NewRecorder()
	handlerFor(s).serve(w, httptest.NewRequest(http.MethodGet, target, nil), view)
	if w.Code != 404 || strings.Contains(w.Body.String(), s.asset.Filename) {
		t.Fatal("deleted asset was selectable", w.Code, w.Body.String())
	}
	s.asset.State = "ready"
	s.err = achrix.ErrDenied
	w = httptest.NewRecorder()
	handlerFor(s).serve(w, httptest.NewRequest(http.MethodGet, target, nil), view)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "permission_denied") {
		t.Fatal("revoked selection permission was bypassed", w.Code, w.Body.String())
	}
	s.err = media.ErrNotFound
	w = httptest.NewRecorder()
	handlerFor(s).serve(w, httptest.NewRequest(http.MethodGet, target, nil), view)
	if w.Code != 404 {
		t.Fatal("stale selection must not succeed", w.Code)
	}
}

func TestPickerCatalogFromComposedMediaLocale(t *testing.T) {
	for _, language := range []string{"en", "fa"} {
		t.Run(language, func(t *testing.T) {
			v := requestView()
			v.Language = language
			v.Localizer = presentationLocalizer(t, language)
			w := httptest.NewRecorder()
			handlerFor(&libraryStub{}).serve(w, httptest.NewRequest(http.MethodGet, "/picker/messages", nil), v)
			var body struct {
				Locale   string            `json:"locale"`
				Messages map[string]string `json:"messages"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil ||
				body.Locale != language || len(body.Messages) != len(pickerMessageKeys) ||
				w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatal("picker-only selected-locale catalog contract", w.Code, w.Body.String())
			}
			for _, key := range pickerMessageKeys {
				expected, err := v.Localizer.Text("media", "picker."+key, nil)
				if err != nil || body.Messages[key] != expected {
					t.Fatal("picker message is not catalog-backed", language, key, body.Messages[key], err)
				}
			}
			if _, leaked := body.Messages["selected"]; leaked {
				t.Fatal("parameterized selection message belongs to exact selection response")
			}
			s := &libraryStub{asset: sample()}
			w = httptest.NewRecorder()
			handlerFor(s).serve(w, httptest.NewRequest(http.MethodGet, "/picker/asset/"+s.asset.ID, nil), v)
			var chosen struct {
				Asset   asset  `json:"asset"`
				Message string `json:"message"`
			}
			expected, err := v.Localizer.Text("media", "picker.selected", map[string]string{"Filename": s.asset.Filename})
			if err != nil || w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &chosen) != nil ||
				chosen.Message != expected || chosen.Asset.ID != s.asset.ID || !strings.Contains(chosen.Message, s.asset.Filename) {
				t.Fatal("exact choice lost request-selected locale", w.Code, w.Body.String(), err)
			}
		})
	}

	// Failure to compose an installed locale must not fall back to the wrong language.
	w := httptest.NewRecorder()
	handlerFor(&libraryStub{}).serve(w, httptest.NewRequest(http.MethodGet, "/picker/messages", nil), requestView())
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "picker.open") {
		t.Fatal("missing localizer must fail closed, not manufacture English text", w.Code, w.Body.String())
	}
}

func TestPickerScriptAndLocalizedCatalog(t *testing.T) {
	w := httptest.NewRecorder()
	handlerFor(&libraryStub{}).serve(w, httptest.NewRequest(http.MethodGet, "/picker.js", nil), requestView())
	if w.Code != 200 || !strings.Contains(w.Body.String(), `customElements.define("achrix-media-picker"`) ||
		!strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatal("reusable web component is not served by Media", w.Code, w.Header())
	}
	for _, language := range []string{"en", "fa"} {
		locale := presentationLocalizer(t, language)
		for _, key := range []string{"picker.open", "picker.select", "picker.selected", "picker.unavailable"} {
			message, err := locale.Text("media", key, map[string]string{"Filename": "تصویر.png"})
			if err != nil || message == "" {
				t.Fatal("picker catalog missing", language, key, err)
			}
		}
	}
}
