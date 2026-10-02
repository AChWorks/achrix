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
	return shell.Surface{ID: "identity", Title: shell.Text{English: "Accounts", Persian: "حساب\u200cها"}, Capability: identity.AccountRead, Handler: func(w http.ResponseWriter, r *http.Request, view shell.Request) { serve(service, w, r, view) }}
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
	Language  string
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
			err := view.Render(shell.Page{Title: (shell.Text{English: "Accounts", Persian: "حساب\u200cها"}).In(view.Language), Template: accountTemplate, Data: screen{view.Language, view.Allowed(identity.AccountCreate, "")}})
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
var accountTemplate = template.Must(template.New("accounts").Parse(`{{define "content"}}
<script src="/admin/identity/identity.js" defer></script>
<p>{{if eq .Language "fa"}}شناسهٔ حساب را از نتیجهٔ ایجاد نگه دارید؛ جستجوی فهرست کاربران در این نسخه وجود ندارد.{{else}}Keep the account ID returned by creation. This baseline does not enumerate accounts.{{end}}</p>
<section aria-labelledby="create-heading"><h2 id="create-heading">{{if eq .Language "fa"}}ایجاد حساب{{else}}Create account{{end}}</h2>
{{if .CanCreate}}<form data-admin-form data-operation="create" action="/admin/identity/create" method="post">
<label for="new-login">{{if eq .Language "fa"}}شناسهٔ ورود{{else}}Login{{end}}</label><input id="new-login" name="login" dir="ltr" autocomplete="off" maxlength="64" pattern="[a-z0-9][a-z0-9._\-]{0,63}" required aria-describedby="login-help"><p id="login-help" lang="en" dir="ltr">1–64 lowercase ASCII letters, digits, dot, underscore or hyphen; starts with a letter or digit.</p>
<label for="new-password">{{if eq .Language "fa"}}رمز اولیه{{else}}Initial password{{end}}</label><input id="new-password" name="password" type="password" autocomplete="new-password" maxlength="512" required aria-describedby="password-help"><p id="password-help">{{if eq .Language "fa"}}۱۵ تا ۲۵۶ کاراکتر، حداکثر ۱۰۲۴ بایت. رمز نمایش یا ذخیرهٔ مرورگر نمی` + "\u200c" + `شود.{{else}}15–256 characters, at most 1024 UTF-8 bytes. Passwords are never displayed or stored by this UI.{{end}}</p>
<button type="submit" disabled>{{if eq .Language "fa"}}ایجاد{{else}}Create{{end}}</button></form>{{else}}<p>{{if eq .Language "fa"}}مجوز ایجاد حساب را ندارید.{{else}}Account creation is not permitted.{{end}}</p>{{end}}</section>
<section aria-labelledby="lookup-heading"><h2 id="lookup-heading">{{if eq .Language "fa"}}بررسی یک حساب{{else}}Look up one account{{end}}</h2>
<form data-admin-form data-read-only data-operation="lookup" action="/admin/identity/lookup" method="post"><label for="lookup-id">{{if eq .Language "fa"}}شناسهٔ حساب{{else}}Account ID{{end}}</label><input id="lookup-id" name="id" dir="ltr" maxlength="26" minlength="26" pattern="[A-Z2-7]{26}" required autocomplete="off"><button type="submit" disabled>{{if eq .Language "fa"}}بررسی{{else}}Look up{{end}}</button></form>
<form data-admin-form data-read-only data-operation="lookup-login" action="/admin/identity/lookup-login" method="post"><label for="lookup-login">{{if eq .Language "fa"}}شناسهٔ ورود دقیق برای بررسی نتیجهٔ ایجاد{{else}}Exact login to reconcile creation{{end}}</label><input id="lookup-login" name="login" dir="ltr" maxlength="64" pattern="[a-z0-9][a-z0-9._\-]{0,63}" required autocomplete="off" aria-describedby="lookup-login-help"><p id="lookup-login-help">{{if eq .Language "fa"}}این بررسی مجوز جداگانه می` + "\u200c" + `خواهد. اگر نتیجهٔ ایجاد نامعلوم است، شناسهٔ ورود را بررسی کنید؛ ایجاد را تکرار نکنید.{{else}}This lookup requires a separate permission. After an unknown create outcome, check the retained exact login before another creation attempt.{{end}}</p><button type="submit" disabled>{{if eq .Language "fa"}}بررسی شناسهٔ ورود{{else}}Look up login{{end}}</button></form>
<p id="account-empty">{{if eq .Language "fa"}}هنوز حسابی انتخاب نشده است.{{else}}No account selected.{{end}}</p>
<div id="account" hidden><h3>{{if eq .Language "fa"}}حساب انتخاب` + "\u200c" + `شده{{else}}Selected account{{end}}</h3><dl><dt>ID</dt><dd><bdi id="account-id" dir="ltr"></bdi></dd><dt>{{if eq .Language "fa"}}ورود{{else}}Login{{end}}</dt><dd><bdi id="account-login" dir="auto"></bdi></dd><dt>{{if eq .Language "fa"}}وضعیت{{else}}Status{{end}}</dt><dd id="account-enabled"></dd><dt>{{if eq .Language "fa"}}بازبینی{{else}}Revision{{end}}</dt><dd><bdi id="account-revision" dir="ltr"></bdi></dd></dl>
<form data-admin-form data-operation="enabled" action="/admin/identity/enabled" method="post" hidden><fieldset><legend>{{if eq .Language "fa"}}تغییر وضعیت و لغو نشست` + "\u200c" + `ها{{else}}Change status and revoke sessions{{end}}</legend><input name="id" type="hidden"><input name="revision" type="hidden"><label><input type="checkbox" name="enabled">{{if eq .Language "fa"}}فعال{{else}}Enabled{{end}}</label><label><input type="checkbox" data-confirm required>{{if eq .Language "fa"}}تغییر وضعیت و خروج همهٔ نشست` + "\u200c" + `های این حساب را تأیید می` + "\u200c" + `کنم.{{else}}I confirm the status change and invalidation of every session for this account.{{end}}</label><button type="submit" disabled>{{if eq .Language "fa"}}اعمال وضعیت{{else}}Apply status{{end}}</button></fieldset></form>
<form data-admin-form data-operation="password" action="/admin/identity/password" method="post" hidden><fieldset><legend>{{if eq .Language "fa"}}جایگزینی رمز و لغو نشست` + "\u200c" + `ها{{else}}Replace password and revoke sessions{{end}}</legend><input name="id" type="hidden"><input name="revision" type="hidden"><label for="replacement-password">{{if eq .Language "fa"}}رمز جدید{{else}}New password{{end}}</label><input id="replacement-password" name="password" type="password" autocomplete="new-password" maxlength="512" required><label><input type="checkbox" data-confirm required>{{if eq .Language "fa"}}جایگزینی رمز و خروج همهٔ نشست` + "\u200c" + `ها را تأیید می` + "\u200c" + `کنم.{{else}}I confirm password replacement and invalidation of every session.{{end}}</label><button type="submit" disabled>{{if eq .Language "fa"}}جایگزینی رمز{{else}}Replace password{{end}}</button></fieldset></form>
<form data-admin-form data-operation="revoke" action="/admin/identity/revoke" method="post" hidden><fieldset><legend>{{if eq .Language "fa"}}لغو همهٔ نشست` + "\u200c" + `ها{{else}}Revoke all sessions{{end}}</legend><input name="id" type="hidden"><label><input type="checkbox" data-confirm required>{{if eq .Language "fa"}}خروج همهٔ نشست` + "\u200c" + `های این حساب را تأیید می` + "\u200c" + `کنم.{{else}}I confirm invalidation of every session for this account.{{end}}</label><button type="submit" disabled>{{if eq .Language "fa"}}لغو نشست` + "\u200c" + `ها{{else}}Revoke sessions{{end}}</button></fieldset></form>
<p id="account-no-actions" hidden>{{if eq .Language "fa"}}هیچ تغییر مدیریتی برای این حساب مجاز نیست.{{else}}No management changes are permitted for this account.{{end}}</p></div></section>
{{end}}`))
