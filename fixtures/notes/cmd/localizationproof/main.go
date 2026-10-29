// SPDX-License-Identifier: MPL-2.0
// localizationproof is a bounded test-only consumer of the public Admin APIs.
// Its fixed synthetic principal is not an authentication adapter for deployment.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/admin"
	"github.com/AChWorks/achrix/admin/localize"
)

//go:embed locales/*.json
var resources embed.FS

var content = template.Must(template.New("notes").Parse(`{{define "content"}}<p id="greeting">{{.Greeting}}</p><p id="count">{{.Count}}</p>{{end}}`))

type proofAccess struct{}

func (proofAccess) AuthenticateRequest(*http.Request, bool) (achrix.Principal, error) {
	return "localization-proof", nil
}
func (proofAccess) Authorize(_ context.Context, principal achrix.Principal, capability, target string) error {
	if principal == "localization-proof" && capability == "notes.proof" && target == "" {
		return nil
	}
	return achrix.ErrDenied
}

type report struct {
	Locales []localize.Locale `json:"locales"`
	HTML    string            `json:"html"`
}

func run() error {
	directory := flag.String("catalog-dir", "", "optional directory of additional data-only catalogs")
	language := flag.String("language", "", "product-owned administration language selection; empty defaults to English")
	name := flag.String("name", "Proof", "plain user content for a named parameter")
	count := flag.Int64("count", 0, "plural count")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected argument")
	}
	catalogs, err := fs.Sub(resources, "locales")
	if err != nil {
		return err
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	origin := "https://" + server.Listener.Addr().String()
	config := admin.Config{
		Origin:   origin,
		AuthPath: "/auth",
		// This request header is only this synthetic product's selection policy.
		// Real products authenticate and persist their own permitted preference.
		SelectLanguage: func(r *http.Request) string { return r.Header.Get("X-Proof-Language") },
	}
	if *directory != "" {
		config.Catalogs = []fs.FS{os.DirFS(*directory)}
	}
	surface := admin.Surface{
		ID: "notes", Title: admin.Text{Domain: "notes", Key: "title"},
		Capability: "notes.proof", Catalogs: catalogs,
		Handler: func(w http.ResponseWriter, _ *http.Request, view admin.Request) {
			greeting, e := view.Localizer.Text("notes", "greeting", map[string]string{"Name": *name})
			if e != nil {
				http.Error(w, "localization failed", http.StatusInternalServerError)
				return
			}
			total, e := view.Localizer.Plural("notes", "count", *count, nil)
			if e != nil {
				http.Error(w, "localization failed", http.StatusInternalServerError)
				return
			}
			title, e := view.Localizer.Text("notes", "title", nil)
			if e != nil {
				http.Error(w, "localization failed", http.StatusInternalServerError)
				return
			}
			if e = view.Render(admin.Page{Title: title, Template: content, Data: struct{ Greeting, Count string }{greeting, total}}); e != nil {
				http.Error(w, "render failed", http.StatusInternalServerError)
			}
		},
	}
	shell, err := admin.New(config, proofAccess{}, proofAccess{}, surface)
	if err != nil {
		server.Close()
		return err
	}
	server.Config.Handler = shell.Handler()
	admin.ConfigureServer(server.Config)
	server.StartTLS()
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/admin/notes", nil)
	if err != nil {
		return err
	}
	request.Header.Set("X-Proof-Language", *language)
	client := server.Client()
	client.Timeout = 5 * time.Second
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || len(data) > 128<<10 {
		return fmt.Errorf("proof response status %d or body bound failed", response.StatusCode)
	}
	return json.NewEncoder(os.Stdout).Encode(report{shell.Locales(), string(data)})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "localization proof:", err)
		os.Exit(1)
	}
}
