// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/media"
)

const testAssetID = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

func expectFault(t *testing.T, err error, status int) *bridge.Fault {
	t.Helper()
	var fault *bridge.Fault
	if !errors.As(err, &fault) || fault.HTTPStatus != status || fault.Code == "" || fault.Message == "" || fault.Action == "" || fault.Cause == nil {
		t.Fatalf("expected actionable protected fault with status %d; got %#v (%v)", status, fault, err)
	}
	return fault
}

func jsonRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://product.example/api/media/reconcile", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestDispositionAcceptsOneUnambiguousFilename(t *testing.T) {
	for _, tc := range []struct{ header, filename string }{
		{`attachment; filename=example.png`, "example.png"},
		{`attachment; filename="a;b.txt"`, "a;b.txt"},
		{`ATTACHMENT; FILENAME="a\"b.txt"`, `a"b.txt`},
		{`attachment; filename*=UTF-8''report%20one.txt`, "report one.txt"},
		{`attachment; filename*=utf-8'fa'%DA%AF%D8%B2%D8%A7%D8%B1%D8%B4.txt`, "گزارش.txt"},
	} {
		t.Run(tc.header, func(t *testing.T) {
			actual, err := attachmentFilename(tc.header)
			if err != nil || actual != tc.filename {
				t.Fatal("supported filename changed", actual, err)
			}
		})
	}
	for _, raw := range []string{
		"", "attachment", "inline; filename=a.png", "form-data; name=file; filename=a.png",
		`attachment; filename=""`, `attachment; filename=a.png; filename=a.png`,
		`attachment; filename="a.png"; filename="a.png"`,
		`attachment; filename=a.png; filename*=UTF-8''a.png`,
		`attachment; filename*0*=UTF-8''part; filename*1*=two.png`,
		`attachment; filename*=ISO-8859-1''one.png`,
		`attachment; filename*="UTF-8''one.png"`,
		`attachment; filename*=UTF-8''broken%ZZ.png`,
		`attachment; filename="unterminated.png`,
		`attachment; filename="a.png", attachment; filename="a.png"`,
		"attachment; filename=one.png\r\nX-Principal: admin",
		"attachment; filename=" + strings.Repeat("a", 2049),
	} {
		if _, err := attachmentFilename(raw); err == nil {
			t.Fatalf("ambiguous or unsupported disposition accepted: %q", raw)
		} else if f := expectFault(t, err, 400); f.Outcome != bridge.NotExecuted || f.Fields["filename"] == "" {
			t.Fatal("invalid disposition lacks safe field guidance", f)
		}
	}
}

type observedBody struct {
	reader io.Reader
	reads  int
}

func (b *observedBody) Read(p []byte) (int, error) { b.reads++; return b.reader.Read(p) }
func (*observedBody) Close() error                 { return nil }

type repeatedByte struct{}

func (repeatedByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func uploadRequest(body io.Reader) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://product.example/api/media/assets", body)
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("Content-Disposition", `attachment; filename="example.txt"`)
	return r
}

func TestUploadDecodeDoesNotReadOrHideOwningOversizeProbe(t *testing.T) {
	body := &observedBody{reader: io.LimitReader(repeatedByte{}, media.MaxUploadBytes+1)}
	r := uploadRequest(body)
	r.ContentLength = -1
	input, err := decodeUpload(r)
	if err != nil || input.body != body || body.reads != 0 || input.filename != "example.txt" {
		t.Fatal("transport read or replaced the original upload before dispatch", input, body.reads, err)
	}
	n, err := io.Copy(io.Discard, input.body)
	if err != nil || n != media.MaxUploadBytes+1 {
		t.Fatal("transport hid the byte needed by the Service's oversize probe", n, err)
	}
	body = &observedBody{reader: strings.NewReader("not read")}
	r = uploadRequest(body)
	r.ContentLength = media.MaxUploadBytes + 1
	_, err = decodeUpload(r)
	fault := expectFault(t, err, http.StatusRequestEntityTooLarge)
	if body.reads != 0 || fault.Outcome != bridge.NotExecuted {
		t.Fatal("known oversize input was read before rejection")
	}
	// Bridge wraps even http.NoBody. Known length, rather than reader identity,
	// is the cheap evidence that this upload cannot contain an original.
	body = &observedBody{reader: http.NoBody}
	r = uploadRequest(body)
	r.ContentLength = 0
	_, err = decodeUpload(r)
	if f := expectFault(t, err, 400); f.Fields["file"] == "" || f.Outcome != bridge.NotExecuted || body.reads != 0 {
		t.Fatal("known empty wrapped body reached owning Create", f)
	}
}

func TestUploadRejectsAmbiguousMetadataBeforeReading(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		status int
	}{
		{"duplicate disposition", func(r *http.Request) { r.Header.Add("Content-Disposition", `attachment; filename="example.txt"`) }, 400},
		{"duplicate content type", func(r *http.Request) { r.Header.Add("Content-Type", "application/octet-stream") }, 400},
		{"content type parameter", func(r *http.Request) { r.Header.Set("Content-Type", "application/octet-stream; charset=utf-8") }, 415},
		{"content encoding", func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }, 415},
		{"range", func(r *http.Request) { r.Header.Set("Range", "bytes=0-1") }, 400},
		{"if range", func(r *http.Request) { r.Header.Set("If-Range", "opaque") }, 400},
		{"trailers", func(r *http.Request) { r.Trailer = http.Header{"X-Checksum": {"value"}} }, 400},
		{"transfer transformation", func(r *http.Request) { r.TransferEncoding = []string{"gzip", "chunked"} }, 400},
		{"unknown actor", func(r *http.Request) { r.URL.RawQuery = "actor=admin" }, 400},
		{"wrong method", func(r *http.Request) { r.Method = http.MethodPut }, 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &observedBody{reader: strings.NewReader("unread")}
			r := uploadRequest(body)
			r.ContentLength = -1
			tc.change(r)
			_, err := decodeUpload(r)
			expectFault(t, err, tc.status)
			if body.reads != 0 {
				t.Fatal("invalid upload metadata consumed original bytes")
			}
		})
	}
}

func TestHTTPJSONRequiresOneExactBoundedObject(t *testing.T) {
	for _, body := range []string{
		``, `null`, `[]`, `{"limit":1,"limit":1}`, `{"limit":1,"\u006cimit":2}`,
		`{"actor":"admin"}`, `{"limit":1} {}`, "{}x", "{}\x00", `{"limit":1`,
		`{"limit":null}`, `{"limit":1.5}`, `{"limit":"1"}`,
	} {
		_, err := decodeReconcile(jsonRequest(body))
		if f := expectFault(t, err, 400); f.Outcome != bridge.NotExecuted || len(f.Fields) == 0 {
			t.Fatalf("unsafe or ambiguous JSON lacked pre-dispatch guidance: %q", body)
		}
	}
	for _, body := range []string{`{}`, `{"limit":1}`, `{"limit":40}`, "{}" + strings.Repeat(" ", maxJSONBytes-2)} {
		if _, err := decodeReconcile(jsonRequest(body)); err != nil {
			t.Fatal("valid JSON rejected", err)
		}
	}
	_, err := decodeReconcile(jsonRequest("{}" + strings.Repeat(" ", maxJSONBytes-1)))
	expectFault(t, err, http.StatusRequestEntityTooLarge)
	for _, body := range []string{`{}`, `{"expected_revision":9007199254740993}`, `{"expected_revision":null}`, `{"expected_revision":"1","asset_id":"` + testAssetID + `"}`} {
		_, err := decodeDelete(jsonRequest(body), testAssetID)
		expectFault(t, err, 400)
	}
	input, err := decodeDelete(jsonRequest(`{"expected_revision":"9007199254740993"}`), testAssetID)
	if err != nil || input.ExpectedRevision != "9007199254740993" || input.AssetID != testAssetID {
		t.Fatal("large revision was not preserved exactly", input, err)
	}
}

type bytesWithFailure struct{ cause error }

func (r bytesWithFailure) Read(p []byte) (int, error) { return copy(p, "{}"), r.cause }

func TestHTTPJSONRetainsBodyFailureEvenAfterCompleteObject(t *testing.T) {
	private := errors.New("PRIVATE_BODY_FAILURE")
	r := jsonRequest("{}")
	r.Body = io.NopCloser(bytesWithFailure{private})
	_, err := decodeReconcile(r)
	fault := expectFault(t, err, 503)
	if !errors.Is(fault.Cause, private) || fault.Outcome != bridge.NotExecuted || strings.Contains(fault.Message, private.Error()) {
		t.Fatal("body failure became success or leaked its protected cause", fault)
	}
}

func TestReadInputRejectsDuplicateUnknownAndHiddenBodies(t *testing.T) {
	for _, query := range []string{"limit=1&limit=1", "cursor=&cursor=", "actor=admin", "limit=abc", "cursor=%GG", "cursor=" + strings.Repeat("a", 513)} {
		r := httptest.NewRequest(http.MethodGet, "https://product.example/api/media/assets", nil)
		r.URL.RawQuery = query
		_, err := decodeList(r)
		expectFault(t, err, 400)
	}
	for _, length := range []int64{-1, 1} {
		r := httptest.NewRequest(http.MethodGet, "https://product.example/api/media/assets", strings.NewReader("x"))
		r.ContentLength = length
		_, err := decodeList(r)
		expectFault(t, err, 400)
	}
	input, err := decodeList(httptest.NewRequest(http.MethodGet, "https://product.example/api/media/assets", nil))
	if err != nil || input.Limit != nil {
		t.Fatal("absent limit no longer selects owning adapter default", input, err)
	}
}
