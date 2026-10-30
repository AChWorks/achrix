// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/media"
)

const maxJSONBytes = 4096

type uploadInput struct {
	filename string
	body     io.Reader
}

func requestControls(r *http.Request, method string) error {
	if r.Method != method {
		return &bridge.Fault{Code: "method_not_allowed", Message: "This method is not supported for the selected Media route.",
			Action: "Use the method advertised in Allow and the published route contract.", Outcome: bridge.NotExecuted,
			HTTPStatus: http.StatusMethodNotAllowed, Cause: errors.New("wrong method for explicit Media route")}
	}
	for _, name := range []string{"Content-Type", "Content-Disposition", "Content-Encoding", "Content-Length", "Range", "If-Range", "Trailer"} {
		if len(r.Header.Values(name)) > 1 {
			return inputFault("headers", "Send at most one value for each content, transfer and range control header.", errors.New("ambiguous Media control header"))
		}
	}
	if r.Header.Get("Content-Encoding") != "" {
		return &bridge.Fault{Code: "unsupported_content_encoding", Message: "Encoded request bodies are not supported by this Media profile.",
			Action: "Send the original bounded bytes or JSON without Content-Encoding.", Outcome: bridge.NotExecuted,
			HTTPStatus: http.StatusUnsupportedMediaType, Cause: errors.New("unsupported Media Content-Encoding")}
	}
	if r.Header.Get("Range") != "" || r.Header.Get("If-Range") != "" {
		return inputFault("range", "This profile transfers complete originals and does not implement Range or If-Range.", errors.New("range request outside Media transfer profile"))
	}
	if r.Header.Get("Trailer") != "" || len(r.Trailer) != 0 || len(r.TransferEncoding) > 1 || len(r.TransferEncoding) == 1 && r.TransferEncoding[0] != "chunked" {
		return inputFault("body", "Use a plain or chunked body without trailers or transfer transformations.", errors.New("unsupported Media body framing"))
	}
	return nil
}

func noBody(r *http.Request) error {
	if r.ContentLength > 0 {
		return inputFault("body", "Send no request body for this read.", errors.New("nonempty Media read body"))
	}
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	n, err := io.CopyN(io.Discard, r.Body, 1)
	if n != 0 {
		return inputFault("body", "Send no request body for this read.", errors.New("nonempty Media read body"))
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return bodyFault(err)
	}
	return nil
}

func readInput(r *http.Request) error {
	if err := requestControls(r, http.MethodGet); err != nil {
		return err
	}
	return noBody(r)
}

func queryFields(r *http.Request, allowed ...string) (url.Values, error) {
	if len(r.URL.RawQuery) > 512 {
		return nil, inputFault("query", "Use at most 512 encoded bytes for the supported query fields.", errors.New("media query exceeds bound"))
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, inputFault("query", "Use correctly encoded query parameters.", err)
	}
	for name, entries := range values {
		known := false
		for _, field := range allowed {
			if name == field {
				known = true
				break
			}
		}
		if !known {
			return nil, inputFault("query", "Use only the published route parameters; actor and authority claims are not accepted.", errors.New("unknown Media query parameter"))
		}
		if len(entries) != 1 {
			return nil, inputFault(name, "Send this query parameter at most once.", errors.New("duplicate Media query parameter"))
		}
	}
	return values, nil
}

func decodeList(r *http.Request) (ListInput, error) {
	if err := readInput(r); err != nil {
		return ListInput{}, err
	}
	values, err := queryFields(r, "cursor", "limit")
	if err != nil {
		return ListInput{}, err
	}
	input := ListInput{Cursor: values.Get("cursor")}
	if raw, present := values["limit"]; present {
		limit, err := strconv.Atoi(raw[0])
		if err != nil {
			return ListInput{}, inputFault("limit", "Use an integer in 1..100, or omit it to select 20.", err)
		}
		input.Limit = &limit
	}
	return input, nil
}

func decodeUpload(r *http.Request) (uploadInput, error) {
	if err := requestControls(r, http.MethodPost); err != nil {
		return uploadInput{}, err
	}
	if _, err := queryFields(r); err != nil {
		return uploadInput{}, err
	}
	if r.ContentLength > media.MaxUploadBytes {
		return uploadInput{}, &bridge.Fault{Code: "request_too_large", Message: "The upload exceeds the 10 MiB Media limit.",
			Action: "Send a complete supported file of at most 10 MiB.", Outcome: bridge.NotExecuted,
			HTTPStatus: http.StatusRequestEntityTooLarge, Cause: errors.New("known Media Content-Length exceeds file limit")}
	}
	if r.ContentLength == 0 {
		return uploadInput{}, inputFault("file", "Send a nonempty original file body.", errors.New("known empty Media upload"))
	}
	if err := contentType(r, "application/octet-stream"); err != nil {
		return uploadInput{}, err
	}
	if len(r.Header.Values("Content-Disposition")) != 1 {
		return uploadInput{}, inputFault("filename", "Send one Content-Disposition: attachment header with exactly one filename or UTF-8 filename* parameter.", errors.New("missing or repeated upload disposition"))
	}
	filename, err := attachmentFilename(r.Header.Get("Content-Disposition"))
	if err != nil {
		return uploadInput{}, err
	}
	if r.Body == nil || r.Body == http.NoBody {
		return uploadInput{}, inputFault("file", "Send a nonempty original file body.", errors.New("missing Media upload body"))
	}
	// No full-file buffering or pre-gate body read. Bridge owns the actual
	// socket deadline and request cap; Service.Create performs the +1 probe.
	return uploadInput{filename: filename, body: r.Body}, nil
}

func contentType(r *http.Request, expected string) error {
	if len(r.Header.Values("Content-Type")) != 1 {
		return inputFault("content_type", "Send exactly one Content-Type matching the operation's published body.", errors.New("missing or repeated Media Content-Type"))
	}
	kind, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != expected || len(params) != 0 {
		return &bridge.Fault{Code: "unsupported_content_type", Message: "The request has an unsupported content type or parameter.",
			Action: "Use " + expected + " without content-type parameters for this operation.", Outcome: bridge.NotExecuted,
			HTTPStatus: http.StatusUnsupportedMediaType, Cause: errors.New("unexpected Media request media type")}
	}
	return nil
}

func attachmentFilename(raw string) (string, error) {
	bad := func() (string, error) {
		return "", inputFault("filename", "Use attachment with exactly one filename or UTF-8 filename* parameter; no continuations or duplicate parameters.", errors.New("invalid strict upload disposition"))
	}
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n") {
		return bad()
	}
	kind, rest, ok := strings.Cut(raw, ";")
	if !ok || !strings.EqualFold(strings.TrimSpace(kind), "attachment") {
		return bad()
	}
	name, value, ok := strings.Cut(strings.TrimSpace(rest), "=")
	name, value = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(value)
	if !ok || name != "filename" && name != "filename*" || value == "" {
		return bad()
	}
	if value[0] == '"' {
		if name == "filename*" {
			return bad()
		}
		// The grammar allows a semicolon inside one quoted filename. Find its
		// actual closing quote so duplicate equal parameters cannot be collapsed
		// by mime.ParseMediaType into one apparently legitimate value.
		escaped, closed := false, false
		for i := 1; i < len(value); i++ {
			if escaped {
				escaped = false
				continue
			}
			if value[i] == '\\' {
				escaped = true
				continue
			}
			if value[i] == '"' {
				if strings.TrimSpace(value[i+1:]) != "" {
					return bad()
				}
				closed = true
				break
			}
		}
		if !closed {
			return bad()
		}
	} else if strings.ContainsAny(value, ";\"\r\n") {
		return bad()
	}
	if name == "filename*" {
		charset, _, exists := strings.Cut(value, "'")
		if !exists || !strings.EqualFold(charset, "utf-8") {
			return bad()
		}
	}
	parsedKind, params, err := mime.ParseMediaType(raw)
	if err != nil || parsedKind != "attachment" || len(params) != 1 || params["filename"] == "" {
		return bad()
	}
	return params["filename"], nil
}

func decodeObject(r *http.Request, allowed ...string) (map[string]json.RawMessage, error) {
	if err := requestControls(r, http.MethodPost); err != nil {
		return nil, err
	}
	if _, err := queryFields(r); err != nil {
		return nil, err
	}
	if r.Header.Get("Content-Disposition") != "" {
		return nil, inputFault("headers", "Content-Disposition is only accepted by the raw upload route.", errors.New("unexpected JSON disposition"))
	}
	if err := contentType(r, "application/json"); err != nil {
		return nil, err
	}
	if r.Body == nil {
		return nil, inputFault("body", "Send one JSON object.", errors.New("missing Media JSON body"))
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBytes+1))
	if err != nil {
		return nil, bodyFault(err)
	}
	if len(data) > maxJSONBytes {
		return nil, &bridge.Fault{Code: "request_too_large", Message: "The Media JSON body exceeds 4096 bytes.",
			Action: "Send one bounded object containing only this operation's published fields.", Outcome: bridge.NotExecuted,
			HTTPStatus: http.StatusRequestEntityTooLarge, Cause: errors.New("media JSON body exceeds independent bound")}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, inputFault("body", "Send one JSON object.", errors.New("media JSON is not an object"))
	}
	result := make(map[string]json.RawMessage, len(allowed))
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, inputFault("body", "Send a complete well-formed JSON object.", err)
		}
		name, ok := key.(string)
		if !ok {
			return nil, inputFault("body", "JSON member names must be strings.", errors.New("invalid Media JSON member"))
		}
		known := false
		for _, field := range allowed {
			if name == field {
				known = true
				break
			}
		}
		if !known {
			return nil, inputFault("body", "Use only the published field names; identity/authority claims are not accepted.", errors.New("unknown Media JSON field"))
		}
		if _, duplicate := result[name]; duplicate {
			return nil, inputFault(name, "Send this JSON field exactly once.", errors.New("duplicate Media JSON field"))
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, inputFault(name, "Send a complete JSON value of the published type.", err)
		}
		result[name] = value
	}
	if close, err := decoder.Token(); err != nil || close != json.Delim('}') {
		return nil, inputFault("body", "Complete the JSON object.", errors.New("incomplete Media JSON object"))
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, inputFault("body", "Send exactly one JSON object without trailing values or bytes.", errors.New("trailing Media JSON value or malformed bytes"))
	}
	return result, nil
}

func decodeDelete(r *http.Request, id string) (DeleteInput, error) {
	fields, err := decodeObject(r, "expected_revision")
	if err != nil {
		return DeleteInput{}, err
	}
	var revision string
	if raw, ok := fields["expected_revision"]; !ok || json.Unmarshal(raw, &revision) != nil {
		return DeleteInput{}, inputFault("expected_revision", "Provide the unchanged positive decimal STRING revision from Status.", errors.New("missing or non-string Media revision"))
	}
	if _, err := parseRevision(revision); err != nil {
		return DeleteInput{}, err
	}
	return DeleteInput{AssetID: id, ExpectedRevision: revision}, nil
}

func decodeReconcile(r *http.Request) (ReconcileInput, error) {
	fields, err := decodeObject(r, "limit")
	if err != nil {
		return ReconcileInput{}, err
	}
	input := ReconcileInput{}
	if raw, ok := fields["limit"]; ok {
		var limit *int
		if json.Unmarshal(raw, &limit) != nil || limit == nil {
			return ReconcileInput{}, inputFault("limit", "Use an integer in 1..40; omit the field to select 10.", errors.New("invalid Media reconciliation limit type"))
		}
		input.Limit = limit
	}
	return input, nil
}

func bodyFault(cause error) *bridge.Fault {
	return &bridge.Fault{Code: "body_unavailable", Message: "The request body could not be read completely.",
		Action:  "Check the connection and send one complete bounded request within the deadline.",
		Outcome: bridge.NotExecuted, HTTPStatus: http.StatusServiceUnavailable, Cause: cause}
}
