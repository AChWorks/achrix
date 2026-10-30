// SPDX-License-Identifier: MPL-2.0
package remote

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/AChWorks/achrix/bridge"
	"github.com/AChWorks/achrix/media"
)

// NewHTTP composes explicit private metadata, upload, download, delete and
// reconciliation routes. Zero byte limits select Media's complete 10 MiB file
// profile; explicit lower limits remain product policy. JSON bodies independently
// stay at most 4096 bytes. Nothing starts a listener or per-connection worker.
func NewHTTP(service *media.Service, config bridge.Config) (*bridge.Endpoint, error) {
	if service == nil {
		return nil, errors.New("media remote: service is required")
	}
	resource, err := referenceResource(config.ResourceURL)
	if err != nil {
		return nil, err
	}
	if config.Limits.RequestBytes == 0 {
		config.Limits.RequestBytes = media.MaxUploadBytes + 1
	}
	if config.Limits.ResponseBytes == 0 {
		config.Limits.ResponseBytes = media.MaxUploadBytes
	}
	prefix := strings.TrimSuffix(resource.Path, "/")
	collection := prefix + "/assets"
	listHandler, err := bridge.JSONHandler(ListOperation, decodeList, list(service))
	if err != nil {
		return nil, err
	}
	createHandler, err := bridge.JSONHandler(CreateOperation, decodeUpload, create(service, resource))
	if err != nil {
		return nil, err
	}
	statusHandler, err := bridge.JSONHandler(StatusOperation, func(r *http.Request) (StatusInput, error) {
		if err := readInput(r); err != nil {
			return StatusInput{}, err
		}
		if _, err := queryFields(r); err != nil {
			return StatusInput{}, err
		}
		return StatusInput{AssetID: strings.TrimPrefix(r.URL.Path, collection+"/")}, nil
	}, status(service, resource))
	if err != nil {
		return nil, err
	}
	downloadHandler, err := bridge.StreamHandler(ReadOperation, func(r *http.Request) (StatusInput, error) {
		if err := readInput(r); err != nil {
			return StatusInput{}, err
		}
		if _, err := queryFields(r); err != nil {
			return StatusInput{}, err
		}
		return StatusInput{AssetID: strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, collection+"/"), "/content")}, nil
	}, download(service, config.Limits.ResponseBytes))
	if err != nil {
		return nil, err
	}
	deleteHandler, err := bridge.JSONHandler(DeleteOperation, func(r *http.Request) (DeleteInput, error) {
		return decodeDelete(r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, collection+"/"), "/delete"))
	}, deleteAsset(service))
	if err != nil {
		return nil, err
	}
	reconcileHandler, err := bridge.JSONHandler(ReconcileOperation, decodeReconcile, reconcile(service))
	if err != nil {
		return nil, err
	}
	missing, err := bridge.JSONHandler("achrix.media.http.route", func(*http.Request) (struct{}, error) {
		return struct{}{}, &bridge.Fault{Code: "route_not_found", Message: "This Media HTTP route is not supported.",
			Action: "Use only the routes published for this configured Media resource.", Outcome: bridge.NotExecuted,
			HTTPStatus: http.StatusNotFound, Cause: errors.New("request outside explicit Media routes")}
	}, func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
	if err != nil {
		return nil, err
	}
	return bridge.NewStreamingHTTP(config, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		canonical := r.URL.RawPath == "" && r.URL.Path == path.Clean(r.URL.Path)
		if canonical && r.URL.Path == collection {
			w.Header().Set("Allow", "GET, POST")
			if r.Method == http.MethodGet {
				listHandler.ServeHTTP(w, r)
			} else {
				createHandler.ServeHTTP(w, r)
			}
			return
		}
		if canonical && r.URL.Path == prefix+"/reconcile" {
			w.Header().Set("Allow", http.MethodPost)
			reconcileHandler.ServeHTTP(w, r)
			return
		}
		if canonical && strings.HasPrefix(r.URL.Path, collection+"/") {
			rest := strings.TrimPrefix(r.URL.Path, collection+"/")
			id, suffix, slash := strings.Cut(rest, "/")
			if id != "" && !strings.Contains(suffix, "/") {
				switch {
				case !slash:
					w.Header().Set("Allow", http.MethodGet)
					statusHandler.ServeHTTP(w, r)
					return
				case suffix == "content":
					w.Header().Set("Allow", http.MethodGet)
					downloadHandler.ServeHTTP(w, r)
					return
				case suffix == "delete":
					w.Header().Set("Allow", http.MethodPost)
					deleteHandler.ServeHTTP(w, r)
					return
				}
			}
		}
		missing.ServeHTTP(w, r)
	}))
}

func create(service *media.Service, resource *url.URL) func(context.Context, uploadInput) (AssetInfo, error) {
	return func(ctx context.Context, input uploadInput) (AssetInfo, error) {
		actor, err := principal(ctx)
		if err != nil {
			return AssetInfo{}, err
		}
		asset, err := service.Create(ctx, actor, input.filename, input.body)
		if err != nil {
			return AssetInfo{}, operationFault(err, CreateOperation, asset.ID, nil)
		}
		bridge.Confirm(ctx, asset.ID)
		return present(asset, resource, true), nil
	}
}

type attachmentWriter struct {
	writer  http.ResponseWriter
	asset   media.Asset
	started bool
	cause   error
}

func (w *attachmentWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if !w.started {
		w.writer.Header().Set("Content-Type", w.asset.MIME)
		w.writer.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": w.asset.Filename}))
		w.writer.Header().Set("Content-Length", strconv.FormatInt(w.asset.Size, 10))
		w.writer.Header().Set("Cache-Control", "private, no-store")
		w.writer.Header().Set("X-Content-Type-Options", "nosniff")
		w.started = true
	}
	n, err := w.writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil && w.cause == nil {
		w.cause = err
	}
	return n, err
}

func download(service *media.Service, maximum int64) func(context.Context, StatusInput, http.ResponseWriter) error {
	return func(ctx context.Context, input StatusInput, w http.ResponseWriter) error {
		actor, err := principal(ctx)
		if err != nil {
			return err
		}
		asset, err := service.Status(ctx, actor, input.AssetID)
		if err != nil {
			return operationFault(err, ReadOperation, "", nil)
		}
		if asset.State != "ready" {
			return operationFault(media.ErrConflict, ReadOperation, asset.ID, nil)
		}
		knownMIME := false
		for _, format := range media.SupportedFormats() {
			if format.MIME == asset.MIME {
				knownMIME = true
				break
			}
		}
		if !generatedID.MatchString(asset.ID) || asset.ID != input.AssetID || !knownMIME || asset.Size < 1 || asset.Size > media.MaxUploadBytes {
			return operationFault(errors.New("media download metadata violates the supported private profile"), ReadOperation, "", nil)
		}
		if asset.Size > maximum {
			return &bridge.Fault{Code: "response_too_large", Message: "The original file exceeds this endpoint's configured response budget.",
				Action:  "Ask the product operator to provide a bounded original-byte endpoint supporting this retained file size; no bytes were sent.",
				Outcome: bridge.NotExecuted, HTTPStatus: http.StatusInternalServerError, Reference: asset.ID, Cause: errors.New("original bytes exceed configured response limit")}
		}
		output := &attachmentWriter{writer: w, asset: asset}
		actual, err := service.Read(ctx, actor, input.AssetID, output)
		if err == nil && !output.started {
			err = errors.New("media Read returned no bytes for a nonempty original")
		}
		if err != nil {
			f := operationFault(err, ReadOperation, asset.ID, nil)
			if output.cause != nil {
				f.Cause = errors.Join(err, output.cause)
			}
			// StreamHandler owns post-header abort. Never append a JSON problem
			// to a partially delivered original or hide the destination cause.
			return f
		}
		bridge.Confirm(ctx, actual.ID)
		return nil
	}
}
