// SPDX-License-Identifier: MPL-2.0
// Package admin owns Identity's account-management presentation. It consumes only
// the public Identity Service/Web contracts; storage and credentials stay Identity-owned.
package admin

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"time"

	"github.com/AChWorks/achrix"
	shell "github.com/AChWorks/achrix/admin"
	"github.com/AChWorks/achrix/admin/localize"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/identity"
)

type accounts interface {
	Account(context.Context, achrix.Principal, string) (identity.Account, error)
	LookupAccount(context.Context, achrix.Principal, string) (identity.Account, error)
	CreateAccount(context.Context, achrix.Principal, string, string) (identity.Account, error)
	SetEnabled(context.Context, achrix.Principal, string, int64, bool) error
	SetPassword(context.Context, achrix.Principal, string, int64, string) error
	RevokeAll(context.Context, achrix.Principal, string) error
}

// New registers actual Identity-owned forms. Account enumeration is not offered;
// operators use an opaque ID or separately authorized exact-login reconciliation.
func New(service *identity.Service) (shell.Surface, error) {
	if service == nil {
		return shell.Surface{}, shell.ErrConfiguration
	}
	return surface(service), nil
}
func surface(service accounts) shell.Surface {
	return shell.Surface{ID: "identity", Title: shell.Text{Domain: "identity", Key: "accounts.title"}, Catalogs: locales, Capability: identity.AccountRead, Handler: func(w http.ResponseWriter, r *http.Request, view shell.Request) { serve(service, w, r, view) }}
}

type webAuthenticator struct{ web *identity.Web }

func (a webAuthenticator) AuthenticateRequest(r *http.Request, mutation bool) (achrix.Principal, error) {
	p, err := a.web.AuthenticateRequest(r, mutation)
	if errors.Is(err, identity.ErrAuthentication) {
		return "", shell.ErrUnauthenticated
	}
	return p, err
}

// Authenticator adapts error categories only. Identity.Web still validates the
// existing cookie/CSRF session and returns a principal with no permission grant.
func Authenticator(web *identity.Web) (shell.Authenticator, error) {
	if web == nil {
		return nil, shell.ErrConfiguration
	}
	return webAuthenticator{web}, nil
}

type screen struct {
	Locale    *localize.Localizer
	CanCreate bool
}
type accountResult struct {
	ID       string `json:"id"`
	Login    string `json:"login"`
	Enabled  bool   `json:"enabled"`
	Revision int64  `json:"revision,string"`
}
type result struct {
	Account     accountResult   `json:"account"`
	Permissions map[string]bool `json:"permissions"`
}

func accountResponse(w http.ResponseWriter, a identity.Account, view shell.Request) {
	permissions := map[string]bool{"enabled": view.Allowed(identity.AccountSetEnabled, a.ID), "password": view.Allowed(identity.CredentialSet, a.ID), "revoke": view.Allowed(identity.SessionRevokeAll, a.ID)}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(result{accountResult{a.ID, a.Login, a.Enabled, a.Revision}, permissions})
}

func serve(service accounts, w http.ResponseWriter, r *http.Request, view shell.Request) {
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/":
			title, err := view.Localizer.Text("identity", "accounts.title", nil)
			if err != nil {
				fail(w, shell.ErrConfiguration)
				return
			}
			err = view.Render(shell.Page{Title: title, Template: accountTemplate, Data: screen{view.Localizer, view.Allowed(identity.AccountCreate, "")}})
			if err != nil {
				fail(w, shell.ErrConfiguration)
			}
		case "/identity.js":
			data, _ := assets.ReadFile("assets/identity.js")
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write(data)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var err error
	switch r.URL.Path {
	case "/create":
		var body struct {
			Login    string `json:"login"`
			Password string `json:"password"`
		}
		if err = decode(w, r, &body); err == nil {
			var a identity.Account
			a, err = service.CreateAccount(r.Context(), view.Principal, body.Login, body.Password)
			body.Password = ""
			if err == nil {
				accountResponse(w, a, view)
				return
			}
		}
	case "/lookup":
		var body struct {
			ID string `json:"id"`
		}
		if err = decode(w, r, &body); err == nil {
			var a identity.Account
			a, err = service.Account(r.Context(), view.Principal, body.ID)
			if err == nil {
				accountResponse(w, a, view)
				return
			}
		}
	case "/lookup-login":
		var body struct {
			Login string `json:"login"`
		}
		if err = decode(w, r, &body); err == nil {
			var a identity.Account
			a, err = service.LookupAccount(r.Context(), view.Principal, body.Login)
			if err == nil {
				accountResponse(w, a, view)
				return
			}
		}
	case "/enabled":
		var body struct {
			ID       string `json:"id"`
			Revision int64  `json:"revision,string"`
			Enabled  *bool  `json:"enabled"`
		}
		if err = decode(w, r, &body); err == nil {
			if body.Enabled == nil {
				err = identity.ErrInvalid
			} else {
				err = service.SetEnabled(r.Context(), view.Principal, body.ID, body.Revision, *body.Enabled)
			}
		}
	case "/password":
		var body struct {
			ID       string `json:"id"`
			Revision int64  `json:"revision,string"`
			Password string `json:"password"`
		}
		if err = decode(w, r, &body); err == nil {
			err = service.SetPassword(r.Context(), view.Principal, body.ID, body.Revision, body.Password)
			body.Password = ""
		}
	case "/revoke":
		var body struct {
			ID string `json:"id"`
		}
		if err = decode(w, r, &body); err == nil {
			err = service.RevokeAll(r.Context(), view.Principal, body.ID)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decode(w http.ResponseWriter, r *http.Request, target any) error {
	if len(r.Header.Values("Content-Type")) != 1 || r.Header.Get("Content-Type") != "application/json" {
		return identity.ErrInvalid
	}
	deadline := time.Now().Add(2 * time.Second)
	if d, ok := r.Context().Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if http.NewResponseController(w).SetReadDeadline(deadline) != nil {
		return identity.ErrUnavailable
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return identity.ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return identity.ErrInvalid
	}
	return nil
}
func fail(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "unavailable"
	switch {
	case errors.Is(err, achrix.ErrDenied):
		status, code = 403, "permission_denied"
	case errors.Is(err, identity.ErrAuthentication):
		status, code = 401, "authentication_required"
	case errors.Is(err, identity.ErrInvalid):
		status, code = 400, "invalid_input"
	case errors.Is(err, identity.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, identity.ErrConflict):
		status, code = 409, "conflict"
	case errors.Is(err, identity.ErrLimited):
		status, code = 429, "busy"
	case errors.Is(err, audit.ErrUnavailable), errors.Is(err, identity.ErrUnavailable), errors.Is(err, achrix.ErrAuthorizationUnavailable), errors.Is(err, achrix.ErrNotReady), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{code})
}

//go:embed assets/*
var assets embed.FS

// Each presentation owns its catalogs; the shell composes these at startup.
//
//go:embed locales/*.json
var locales embed.FS
var accountTemplate = template.Must(template.New("accounts").Parse(`{{define "content"}}
<script src="/admin/identity/identity.js" defer></script>
<p>{{.Locale.Text "identity" "accounts.help" nil}}</p>
<section aria-labelledby="create-heading"><h2 id="create-heading">{{.Locale.Text "identity" "create.title" nil}}</h2>
{{if .CanCreate}}<form data-admin-form data-operation="create" action="/admin/identity/create" method="post">
<label for="new-login">{{.Locale.Text "identity" "login.label" nil}}</label><input id="new-login" name="login" dir="ltr" autocomplete="off" maxlength="64" pattern="[a-z0-9][a-z0-9._\-]{0,63}" required aria-describedby="login-help"><p id="login-help">{{.Locale.Text "identity" "login.help" nil}}</p>
<label for="new-password">{{.Locale.Text "identity" "password.initial" nil}}</label><input id="new-password" name="password" type="password" autocomplete="new-password" maxlength="512" required aria-describedby="password-help"><p id="password-help">{{.Locale.Text "identity" "password.help" nil}}</p>
<button type="submit" disabled>{{.Locale.Text "identity" "create.action" nil}}</button></form>{{else}}<p>{{.Locale.Text "identity" "create.denied" nil}}</p>{{end}}</section>
<section aria-labelledby="lookup-heading"><h2 id="lookup-heading">{{.Locale.Text "identity" "lookup.title" nil}}</h2>
<form data-admin-form data-read-only data-operation="lookup" action="/admin/identity/lookup" method="post"><label for="lookup-id">{{.Locale.Text "identity" "account.id" nil}}</label><input id="lookup-id" name="id" dir="ltr" maxlength="26" minlength="26" pattern="[A-Z2-7]{26}" required autocomplete="off"><button type="submit" disabled>{{.Locale.Text "identity" "lookup.action" nil}}</button></form>
<form data-admin-form data-read-only data-operation="lookup-login" action="/admin/identity/lookup-login" method="post"><label for="lookup-login">{{.Locale.Text "identity" "lookup.login.label" nil}}</label><input id="lookup-login" name="login" dir="ltr" maxlength="64" pattern="[a-z0-9][a-z0-9._\-]{0,63}" required autocomplete="off" aria-describedby="lookup-login-help"><p id="lookup-login-help">{{.Locale.Text "identity" "lookup.login.help" nil}}</p><button type="submit" disabled>{{.Locale.Text "identity" "lookup.login.action" nil}}</button></form>
<p id="account-empty">{{.Locale.Text "identity" "account.empty" nil}}</p>
<div id="account" hidden><h3>{{.Locale.Text "identity" "account.selected" nil}}</h3><dl><dt>{{.Locale.Text "identity" "account.id_label" nil}}</dt><dd><bdi id="account-id" dir="ltr"></bdi></dd><dt>{{.Locale.Text "identity" "account.login" nil}}</dt><dd><bdi id="account-login" dir="auto"></bdi></dd><dt>{{.Locale.Text "identity" "account.status" nil}}</dt><dd id="account-enabled"></dd><dt>{{.Locale.Text "identity" "account.revision" nil}}</dt><dd><bdi id="account-revision" dir="ltr"></bdi></dd></dl>
<form data-admin-form data-operation="enabled" action="/admin/identity/enabled" method="post" hidden><fieldset><legend>{{.Locale.Text "identity" "enabled.title" nil}}</legend><input name="id" type="hidden"><input name="revision" type="hidden"><label><input type="checkbox" name="enabled">{{.Locale.Text "identity" "enabled.label" nil}}</label><label><input type="checkbox" data-confirm required>{{.Locale.Text "identity" "enabled.confirm" nil}}</label><button type="submit" disabled>{{.Locale.Text "identity" "enabled.action" nil}}</button></fieldset></form>
<form data-admin-form data-operation="password" action="/admin/identity/password" method="post" hidden><fieldset><legend>{{.Locale.Text "identity" "password.title" nil}}</legend><input name="id" type="hidden"><input name="revision" type="hidden"><label for="replacement-password">{{.Locale.Text "identity" "password.new" nil}}</label><input id="replacement-password" name="password" type="password" autocomplete="new-password" maxlength="512" required><label><input type="checkbox" data-confirm required>{{.Locale.Text "identity" "password.confirm" nil}}</label><button type="submit" disabled>{{.Locale.Text "identity" "password.action" nil}}</button></fieldset></form>
<form data-admin-form data-operation="revoke" action="/admin/identity/revoke" method="post" hidden><fieldset><legend>{{.Locale.Text "identity" "revoke.title" nil}}</legend><input name="id" type="hidden"><label><input type="checkbox" data-confirm required>{{.Locale.Text "identity" "revoke.confirm" nil}}</label><button type="submit" disabled>{{.Locale.Text "identity" "revoke.action" nil}}</button></fieldset></form>
<p id="account-no-actions" hidden>{{.Locale.Text "identity" "account.no_actions" nil}}</p></div></section>
{{end}}`))
