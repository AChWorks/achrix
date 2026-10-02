// SPDX-License-Identifier: MPL-2.0
// Package admin composes trusted, compiled Module-owned administration screens.
// It owns presentation and bounded HTTP admission, never a domain permission or session.
package admin

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/AChWorks/achrix"
)

var (
	ErrConfiguration = errors.New("invalid admin configuration")
	// Product adapters translate only genuine missing/invalid sessions to this
	// error. Other authentication failures are unavailable, never a login prompt.
	ErrUnauthenticated = errors.New("admin authentication required")
)

type Authenticator interface {
	AuthenticateRequest(*http.Request, bool) (achrix.Principal, error)
}
type Authorizer interface {
	Authorize(context.Context, achrix.Principal, string, string) error
}

// Text keeps protocol IDs independent from translated presentation text.
type Text struct{ English, Persian string }

func (t Text) In(language string) string {
	if language == "fa" && t.Persian != "" {
		return t.Persian
	}
	return t.English
}

type Config struct {
	Origin string
	// AuthPath is a same-origin absolute mount for the public Identity.Web handler.
	// The product serves it separately; Admin never proxies credentials.
	AuthPath string
	// Language is en or fa. It changes display only, never domain content.
	Language string
}

// Surface is a finite, instance-owned registration of trusted compiled code.
// Capability/Target gate navigation; every operation still uses its domain Service.
type Surface struct {
	ID                 string
	Title              Text
	Capability, Target string
	Handler            func(http.ResponseWriter, *http.Request, Request)
}
type Page struct {
	Title string
	// Template is compile-time Module-owned HTML with a definition named content.
	Template *template.Template
	Data     any
}
type Request struct {
	Principal achrix.Principal
	Language  string
	Render    func(Page) error
	// Allowed is only a presentation hint; it never grants domain permission.
	Allowed func(capability, target string) bool
}

type Shell struct {
	config   Config
	host     string
	auth     Authenticator
	policy   Authorizer
	surfaces []Surface
	slots    chan struct{}
}

var surfaceSyntax = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func New(config Config, auth Authenticator, policy Authorizer, surfaces ...Surface) (*Shell, error) {
	u, err := url.Parse(config.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.String() != config.Origin || nilInterface(auth) || nilInterface(policy) {
		return nil, ErrConfiguration
	}
	if config.Language == "" {
		config.Language = "en"
	}
	if config.Language != "en" && config.Language != "fa" {
		return nil, ErrConfiguration
	}
	a, err := url.Parse(config.AuthPath)
	if err != nil || !strings.HasPrefix(config.AuthPath, "/") || strings.HasPrefix(config.AuthPath, "//") || a.Path != config.AuthPath || a.RawPath != "" || a.RawQuery != "" || a.Fragment != "" || a.Host != "" || strings.ContainsAny(config.AuthPath, "\\\r\n") || strings.Contains(config.AuthPath, "..") || strings.HasSuffix(config.AuthPath, "/") || config.AuthPath == "/" || config.AuthPath == "/admin" || strings.HasPrefix(config.AuthPath, "/admin/") {
		return nil, ErrConfiguration
	}
	if len(surfaces) == 0 || len(surfaces) > 16 {
		return nil, ErrConfiguration
	}
	seen := make(map[string]bool, len(surfaces))
	for _, s := range surfaces {
		if !surfaceSyntax.MatchString(s.ID) || s.ID == "login" || s.ID == "assets" || seen[s.ID] || s.Title.English == "" || len(s.Title.English) > 100 || len(s.Title.Persian) > 200 || s.Capability == "" || s.Handler == nil {
			return nil, ErrConfiguration
		}
		seen[s.ID] = true
	}
	return &Shell{config: config, host: u.Host, auth: auth, policy: policy, surfaces: append([]Surface(nil), surfaces...), slots: make(chan struct{}, 16)}, nil
}

func (s *Shell) Handler() http.Handler { return http.HandlerFunc(s.serve) }

// ConfigureServer provides the supported ingress bounds before TLS serving.
// The product owns address/TLS keys, listener, graceful Shutdown and Module Stop.
// Compose Admin/Identity/other handlers before calling this helper.
func ConfigureServer(server *http.Server) {
	server.ReadHeaderTimeout = 3 * time.Second
	server.ReadTimeout = 10 * time.Second
	server.WriteTimeout = 12 * time.Second
	server.IdleTimeout = 30 * time.Second
	server.MaxHeaderBytes = 16 << 10
}

func headers(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
}

func headerPresent(headers http.Header, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}
func requestDeadline(ctx context.Context, timeout time.Duration) time.Time {
	deadline := time.Now().Add(timeout)
	if earlier, ok := ctx.Deadline(); ok && earlier.Before(deadline) {
		return earlier
	}
	return deadline
}

func (s *Shell) sameOrigin(r *http.Request) bool {
	if headerPresent(r.Header, "Origin") {
		return len(r.Header.Values("Origin")) == 1 && r.Header.Get("Origin") == s.config.Origin
	}
	u, err := url.Parse(r.Referer())
	return err == nil && u != nil && u.Scheme == "https" && u.Host == s.host && u.User == nil && u.Opaque == "" && len(r.Header.Values("Referer")) == 1
}

func (s *Shell) serve(w http.ResponseWriter, r *http.Request) {
	headers(w)
	if r.TLS == nil || r.Host != s.host || r.URL.RawQuery != "" || r.URL.RawPath != "" || r.URL.Path != "/admin" && !strings.HasPrefix(r.URL.Path, "/admin/") {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		s.fail(w, http.StatusMethodNotAllowed, "invalid_request")
		return
	}
	if r.Method == http.MethodPost && !s.sameOrigin(r) {
		s.fail(w, http.StatusUnauthorized, "authentication_required")
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		s.fail(w, http.StatusTooManyRequests, "busy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	controller := http.NewResponseController(w)
	if controller.SetReadDeadline(requestDeadline(ctx, 8*time.Second)) != nil || controller.SetWriteDeadline(requestDeadline(ctx, 12*time.Second)) != nil {
		s.fail(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if r.ContentLength > 12<<20 {
		s.fail(w, http.StatusRequestEntityTooLarge, "invalid_request")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	defer r.Body.Close()
	if r.URL.Path == "/admin/assets/admin.css" || r.URL.Path == "/admin/assets/admin.js" {
		if r.Method != http.MethodGet {
			s.fail(w, 405, "invalid_request")
			return
		}
		name, kind := "assets/admin.css", "text/css; charset=utf-8"
		if strings.HasSuffix(r.URL.Path, ".js") {
			name, kind = "assets/admin.js", "text/javascript; charset=utf-8"
		}
		data, _ := assets.ReadFile(name)
		w.Header().Set("Content-Type", kind)
		_, _ = w.Write(data)
		return
	}
	if r.URL.Path == "/admin/login" && r.Method == http.MethodGet {
		if err := s.render(w, Page{Title: s.word("Sign in", "ورود"), Template: loginTemplate, Data: s.config}, nil, "login"); err != nil {
			s.fail(w, 503, "unavailable")
		}
		return
	}
	p, err := s.auth.AuthenticateRequest(r, r.Method == http.MethodPost)
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			if r.Method == http.MethodGet {
				w.Header().Set("Location", "/admin/login")
				w.WriteHeader(http.StatusSeeOther)
			} else {
				s.fail(w, 401, "authentication_required")
			}
		} else {
			s.fail(w, 503, "unavailable")
		}
		return
	}
	nav := make([]navItem, 0, len(s.surfaces))
	for _, surface := range s.surfaces {
		err := s.policy.Authorize(ctx, p, surface.Capability, surface.Target)
		if err == nil {
			nav = append(nav, navItem{surface.ID, surface.Title.In(s.config.Language)})
		} else if !errors.Is(err, achrix.ErrDenied) {
			s.fail(w, 503, "unavailable")
			return
		}
	}
	if r.URL.Path == "/admin" || r.URL.Path == "/admin/" {
		if r.Method != http.MethodGet {
			s.fail(w, 405, "invalid_request")
			return
		}
		if err = s.render(w, Page{Title: s.word("Administration", "مدیریت"), Template: homeTemplate, Data: home{len(nav), s.config.Language}}, nav, ""); err != nil {
			s.fail(w, 503, "unavailable")
		}
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/admin/"), "/", 2)
	for _, surface := range s.surfaces {
		if surface.ID != parts[0] {
			continue
		}
		if err = s.policy.Authorize(ctx, p, surface.Capability, surface.Target); err != nil {
			if errors.Is(err, achrix.ErrDenied) {
				s.fail(w, 403, "permission_denied")
			} else {
				s.fail(w, 503, "unavailable")
			}
			return
		}
		copy := r.Clone(ctx)
		copy.URL.Path = "/"
		if len(parts) == 2 {
			copy.URL.Path = "/" + parts[1]
		}
		surface.Handler(w, copy, Request{Principal: p, Language: s.config.Language, Render: func(page Page) error { return s.render(w, page, nav, surface.ID) }, Allowed: func(cap, target string) bool { return s.policy.Authorize(ctx, p, cap, target) == nil }})
		return
	}
	s.fail(w, 404, "not_found")
}

func (s *Shell) word(en, fa string) string { return (Text{en, fa}).In(s.config.Language) }
func (s *Shell) fail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"code":"`+code+`"}`)
}

// Render first completes all trusted templates into bounded buffers. There is
// no partially successful page after template failure and data is autoescaped.
type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 128<<10 {
		return 0, errors.New("admin page too large")
	}
	return b.Buffer.Write(p)
}

type navItem struct{ ID, Title string }
type layout struct {
	Lang, Dir, Title, AuthPath, Active string
	Navigation                         []navItem
	Content                            template.HTML
	SignedIn                           bool
}

func (s *Shell) render(w http.ResponseWriter, page Page, nav []navItem, active string) error {
	if page.Template == nil || page.Template.Lookup("content") == nil {
		return ErrConfiguration
	}
	var content, result boundedBuffer
	if err := page.Template.ExecuteTemplate(&content, "content", page.Data); err != nil {
		return err
	}
	dir := "ltr"
	if s.config.Language == "fa" {
		dir = "rtl"
	}
	view := layout{s.config.Language, dir, page.Title, s.config.AuthPath, active, nav, template.HTML(content.String()), active != "login"} // Only a successfully autoescaped compiled template enters this trusted slot.
	if err := layoutTemplate.Execute(&result, view); err != nil {
		return err
	}
	_, err := w.Write(result.Bytes())
	return err
}

//go:embed assets/*
var assets embed.FS
var layoutTemplate = template.Must(template.New("layout").Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><link rel="stylesheet" href="/admin/assets/admin.css"><script src="/admin/assets/admin.js" defer></script></head>
<body data-auth-path="{{.AuthPath}}"><a class="skip" href="#main">{{if eq .Lang "fa"}}رفتن به محتوا{{else}}Skip to content{{end}}</a><header><a href="/admin/">{{if eq .Lang "fa"}}مدیریت{{else}}Administration{{end}}</a>{{if .SignedIn}}<button type="button" id="logout">{{if eq .Lang "fa"}}خروج{{else}}Sign out{{end}}</button>{{end}}</header>
<div class="shell"><nav aria-label="{{if eq .Lang "fa"}}بخش` + "\u200c" + `های مدیریت{{else}}Administration sections{{end}}"><ul>{{range .Navigation}}<li><a href="/admin/{{.ID}}" {{if eq $.Active .ID}}aria-current="page"{{end}}>{{.Title}}</a></li>{{end}}</ul></nav><main id="main" tabindex="-1"><h1>{{.Title}}</h1><noscript><p>{{if eq .Lang "fa"}}برای ارسال امن فرم` + "\u200c" + `ها JavaScript را فعال کنید.{{else}}Enable JavaScript to submit these forms securely.{{end}}</p></noscript><p id="status" role="status" aria-live="polite" tabindex="-1"></p>{{.Content}}</main></div></body></html>`))

type home struct {
	Count    int
	Language string
}

var homeTemplate = template.Must(template.New("home").Parse(`{{define "content"}}<p>{{if .Count}}{{if eq .Language "fa"}}یک بخش مجاز را از فهرست انتخاب کنید.{{else}}Choose a permitted section from navigation.{{end}}{{else}}{{if eq .Language "fa"}}هیچ بخش مدیریتی برای این حساب مجاز نیست.{{else}}No administration sections are permitted for this account.{{end}}{{end}}</p>{{end}}`))
var loginTemplate = template.Must(template.New("login").Parse(`{{define "content"}}<form data-login action="{{.AuthPath}}/login" method="post"><label for="login">{{if eq .Language "fa"}}شناسهٔ ورود{{else}}Login{{end}}</label><input id="login" name="login" dir="ltr" autocomplete="username" required maxlength="64"><label for="password">{{if eq .Language "fa"}}رمز عبور{{else}}Password{{end}}</label><input id="password" name="password" type="password" autocomplete="current-password" required maxlength="512"><button type="submit" disabled>{{if eq .Language "fa"}}ورود{{else}}Sign in{{end}}</button></form>{{end}}`))
