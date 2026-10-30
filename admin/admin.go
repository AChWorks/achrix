// SPDX-License-Identifier: MPL-2.0
// Package admin composes trusted, compiled Module-owned administration screens.
// It owns presentation and bounded HTTP admission, never a domain permission or session.
package admin

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/admin/localize"
)

var (
	ErrConfiguration = errors.New("invalid admin configuration")
	// ErrResponseDelivery means response writing began and failed. Do not append
	// a fallback response; the request may no longer have a usable transport.
	ErrResponseDelivery = errors.New("admin response delivery failed")
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

// Text identifies a complete catalog message independently of protocol IDs.
// Each owning Module or product supplies the Domain and stable Key.
type Text struct{ Domain, Key string }

type Config struct {
	// Logger receives bounded safe operational evidence. Nil uses slog.Default.
	Logger *slog.Logger
	// Diagnostic receives the original cause before outward mapping. This is a
	// synchronous protected boundary, not a public response or ordinary log.
	// Products must bound the sink, redact provider-specific sensitive fields
	// before retention/export, and control its access and retention.
	Diagnostic func(context.Context, string, string, error)
	Origin     string
	// AuthPath is a same-origin absolute mount for the public Identity.Web handler.
	// The product serves it separately; Admin never proxies credentials.
	AuthPath string
	// Language selects an installed UI locale; empty means English. It never
	// changes stored domain content, canonical values or authorization.
	Language string
	// Catalogs adds product-owned and installed translation data to the snapshot.
	Catalogs []fs.FS
	// SelectLanguage optionally applies product policy to each admitted request.
	// Empty uses Language; an unsupported locale fails closed before dispatch. The
	// product owns preferences, persistence and synchronization of this callback.
	SelectLanguage func(*http.Request) string
}

// Surface is a finite, instance-owned registration of trusted compiled code.
// Capability/Target gate navigation; every operation still uses its domain Service.
type Surface struct {
	ID                 string
	Title              Text
	Catalogs           fs.FS
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
	Localizer *localize.Localizer
	Render    func(Page) error
	// Allowed is only a presentation hint; it never grants domain permission.
	Allowed func(capability, target string) bool
	// CheckAllowed distinguishes an explicit denial (false, nil) from a failed
	// policy evaluation. It is still only presentation, never domain authority.
	CheckAllowed func(capability, target string) (bool, error)
	diagnose     func(operation, phase string, cause error)
}

type Shell struct {
	config      Config
	host        string
	auth        Authenticator
	policy      Authorizer
	surfaces    []Surface
	slots       chan struct{}
	catalog     *localize.Catalog
	browser     map[string]map[string]string
	diagnostics diagnosticWindowState
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
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	u, err := url.Parse(config.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.String() != config.Origin {
		return nil, fmt.Errorf("%w: origin must be an exact HTTPS authority without credentials, path, query or fragment", ErrConfiguration)
	}
	if nilInterface(auth) {
		return nil, fmt.Errorf("%w: authenticator is required", ErrConfiguration)
	}
	if nilInterface(policy) {
		return nil, fmt.Errorf("%w: authorizer is required", ErrConfiguration)
	}
	if config.Language == "" {
		config.Language = "en"
	}
	a, err := url.Parse(config.AuthPath)
	if err != nil || !strings.HasPrefix(config.AuthPath, "/") || strings.HasPrefix(config.AuthPath, "//") || a.Path != config.AuthPath || a.RawPath != "" || a.RawQuery != "" || a.Fragment != "" || a.Host != "" || strings.ContainsAny(config.AuthPath, "\\\r\n") || strings.Contains(config.AuthPath, "..") || strings.HasSuffix(config.AuthPath, "/") || config.AuthPath == "/" || config.AuthPath == "/admin" || strings.HasPrefix(config.AuthPath, "/admin/") {
		return nil, fmt.Errorf("%w: auth path must be a separate absolute same-origin mount", ErrConfiguration)
	}
	if len(surfaces) == 0 || len(surfaces) > 16 {
		return nil, fmt.Errorf("%w: surface count must be between 1 and 16", ErrConfiguration)
	}
	seen := make(map[string]bool, len(surfaces))
	for index, s := range surfaces {
		if !surfaceSyntax.MatchString(s.ID) || s.ID == "login" || s.ID == "assets" || seen[s.ID] || s.Title.Domain == "" || s.Title.Key == "" || s.Capability == "" || s.Handler == nil {
			return nil, fmt.Errorf("%w: surface %d requires a unique valid ID, title, capability and handler", ErrConfiguration, index)
		}
		seen[s.ID] = true
	}
	s := &Shell{config: config, host: u.Host, auth: auth, policy: policy, surfaces: append([]Surface(nil), surfaces...), slots: make(chan struct{}, 16)}
	if err := s.configureCatalogs(); err != nil {
		s.diagnose(context.Background(), "configure", "catalogs", err)
		return nil, err
	}
	return s, nil
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
		s.fail(r.Context(), w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		s.fail(r.Context(), w, http.StatusMethodNotAllowed, "invalid_request")
		return
	}
	if r.Method == http.MethodPost && !s.sameOrigin(r) {
		s.fail(r.Context(), w, http.StatusUnauthorized, "authentication_required")
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		s.fail(r.Context(), w, http.StatusTooManyRequests, "busy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(requestDeadline(ctx, 8*time.Second)); err != nil {
		s.diagnose(ctx, "request", "set_read_deadline", err)
		s.failBefore(r.Context(), w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if err := controller.SetWriteDeadline(requestDeadline(ctx, 12*time.Second)); err != nil {
		s.diagnose(ctx, "request", "set_write_deadline", err)
		s.failBefore(r.Context(), w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if r.ContentLength > 12<<20 {
		s.failBefore(r.Context(), w, http.StatusRequestEntityTooLarge, "input_too_large")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
	defer func() {
		if err := r.Body.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			s.diagnose(ctx, "request", "body_close", err)
		}
	}()
	if name, kind, ok := assetPath(r.URL.Path); ok {
		if r.Method != http.MethodGet {
			s.fail(r.Context(), w, 405, "invalid_request")
			return
		}
		data, err := assets.ReadFile(name)
		if err != nil {
			s.diagnose(ctx, "asset", "read_embedded", err)
			s.failBefore(r.Context(), w, 503, "unavailable")
			return
		}
		w.Header().Set("Content-Type", kind)
		if _, err := w.Write(data); err != nil {
			s.diagnose(ctx, "asset", "response_write", err)
		}
		return
	}
	locale, err := s.requestLocale(r)
	if err != nil {
		s.diagnose(ctx, "localize", "select_locale", err)
		s.failBefore(r.Context(), w, 503, "locale_unavailable")
		return
	}
	if r.URL.Path == "/admin/login" && r.Method == http.MethodGet {
		title, titleErr := locale.Text("admin", "sign_in", nil)
		if titleErr != nil {
			s.diagnose(ctx, "localize", "page_title", titleErr)
			s.failBefore(r.Context(), w, 503, "locale_unavailable")
			return
		}
		if err := s.renderPage(ctx, w, Page{Title: title, Template: loginTemplate, Data: login{locale, s.config.AuthPath}}, nil, "login", locale); err != nil && !errors.Is(err, ErrResponseDelivery) {
			s.fail(r.Context(), w, 503, "unavailable")
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
				s.fail(r.Context(), w, 401, "authentication_required")
			}
		} else {
			s.diagnose(ctx, "authenticate", "provider", err)
			s.failBefore(r.Context(), w, 503, "authentication_unavailable")
		}
		return
	}
	nav := make([]navItem, 0, len(s.surfaces))
	for _, surface := range s.surfaces {
		err := s.policy.Authorize(ctx, p, surface.Capability, surface.Target)
		if err == nil {
			title, titleErr := locale.Text(surface.Title.Domain, surface.Title.Key, nil)
			if titleErr != nil {
				s.diagnose(ctx, "localize", "surface_title", titleErr)
				s.failBefore(r.Context(), w, 503, "locale_unavailable")
				return
			}
			nav = append(nav, navItem{surface.ID, title})
		} else if !errors.Is(err, achrix.ErrDenied) {
			s.diagnose(ctx, "authorize", "navigation_permission", err)
			s.failBefore(r.Context(), w, 503, "authorization_unavailable")
			return
		}
	}
	if r.URL.Path == "/admin" || r.URL.Path == "/admin/" {
		if r.Method != http.MethodGet {
			s.fail(r.Context(), w, 405, "invalid_request")
			return
		}
		title, titleErr := locale.Text("admin", "administration", nil)
		if titleErr != nil {
			s.diagnose(ctx, "localize", "page_title", titleErr)
			s.failBefore(r.Context(), w, 503, "locale_unavailable")
			return
		}
		if err = s.renderPage(ctx, w, Page{Title: title, Template: homeTemplate, Data: home{len(nav), locale}}, nav, "", locale); err != nil && !errors.Is(err, ErrResponseDelivery) {
			s.fail(r.Context(), w, 503, "unavailable")
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
				s.fail(r.Context(), w, 403, "permission_denied")
			} else {
				s.diagnose(ctx, "authorize", "surface_permission", err)
				s.failBefore(r.Context(), w, 503, "authorization_unavailable")
			}
			return
		}
		copy := r.Clone(ctx)
		copy.URL.Path = "/"
		if len(parts) == 2 {
			copy.URL.Path = "/" + parts[1]
		}
		check := func(capability, target string) (bool, error) {
			err := s.policy.Authorize(ctx, p, capability, target)
			if err == nil {
				return true, nil
			}
			if errors.Is(err, achrix.ErrDenied) {
				return false, nil
			}
			s.diagnose(ctx, "authorize", "presentation_permission", err)
			return false, achrix.ErrAuthorizationUnavailable
		}
		surface.Handler(w, copy, Request{Principal: p, Language: locale.Locale().Tag, Localizer: locale,
			Render:       func(page Page) error { return s.renderPage(ctx, w, page, nav, surface.ID, locale) },
			Allowed:      func(capability, target string) bool { allowed, _ := check(capability, target); return allowed },
			CheckAllowed: check,
			diagnose:     func(operation, phase string, cause error) { s.diagnose(ctx, operation, phase, cause) },
		})
		return
	}
	s.fail(r.Context(), w, 404, "not_found")
}

func (s *Shell) failBefore(ctx context.Context, w http.ResponseWriter, status int, code string) {
	s.failureResponse(ctx, w, status, code, "not_executed")
}

func (s *Shell) fail(ctx context.Context, w http.ResponseWriter, status int, code string) {
	s.failureResponse(ctx, w, status, code, "")
}

func (s *Shell) failureResponse(ctx context.Context, w http.ResponseWriter, status int, code, outcome string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	body := `{"code":"` + code + `"`
	if outcome != "" {
		body += `,"outcome":"` + outcome + `"`
	}
	if _, err := io.WriteString(w, body+`}`); err != nil {
		s.diagnose(ctx, "error_response", "response_write", err)
	}
}

// Render first completes all trusted templates into bounded buffers. There is
// no partially successful page after template failure and data is autoescaped.
var errPageLimit = errors.New("admin page exceeds 128 KiB")

type renderFailure struct {
	phase string
	cause error
}

func (e *renderFailure) Error() string { return "admin render failed during " + e.phase }
func (e *renderFailure) Is(target error) bool {
	return target == ErrResponseDelivery && e.phase == "response_write" || target == ErrConfiguration && e.phase == "template_contract"
}

func (s *Shell) renderPage(ctx context.Context, w http.ResponseWriter, page Page, nav []navItem, active string, locale *localize.Localizer) error {
	err := s.render(w, page, nav, active, locale)
	if failure, ok := err.(*renderFailure); ok {
		s.diagnose(ctx, "render", failure.phase, failure.cause)
	}
	return err
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 128<<10 {
		return 0, errPageLimit
	}
	return b.Buffer.Write(p)
}

type navItem struct{ ID, Title string }
type layout struct {
	Lang, Dir, Title, AuthPath, Active string
	Navigation                         []navItem
	Content                            template.HTML
	SignedIn                           bool
	Locale                             *localize.Localizer
	Messages                           string
}

func (s *Shell) render(w http.ResponseWriter, page Page, nav []navItem, active string, locale *localize.Localizer) error {
	if page.Template == nil || page.Template.Lookup("content") == nil {
		return &renderFailure{"template_contract", ErrConfiguration}
	}
	var content, result boundedBuffer
	if err := page.Template.ExecuteTemplate(&content, "content", page.Data); err != nil {
		return &renderFailure{"content_template", err}
	}
	metadata := locale.Locale()
	view := layout{Lang: metadata.Tag, Dir: metadata.Direction, Title: page.Title, AuthPath: s.config.AuthPath, Active: active, Navigation: nav, Content: template.HTML(content.String()), SignedIn: active != "login", Locale: locale, Messages: s.browser[metadata.Tag][active]} // Only a successfully autoescaped compiled template enters this trusted slot.
	if err := layoutTemplate.Execute(&result, view); err != nil {
		return &renderFailure{"layout_template", err}
	}
	n, err := w.Write(result.Bytes())
	if err == nil && n != result.Len() {
		err = io.ErrShortWrite
	}
	if err != nil {
		return &renderFailure{"response_write", err}
	}
	return nil
}

//go:embed assets/*
var assets embed.FS
var layoutTemplate = template.Must(template.New("layout").Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><link rel="stylesheet" href="/admin/assets/admin.css"><script src="/admin/assets/i18n.js" defer></script><script src="/admin/assets/admin.js" defer></script></head>
<body data-auth-path="{{.AuthPath}}"><div id="admin-messages" hidden>{{.Messages}}</div><a class="skip" href="#main">{{.Locale.Text "admin" "skip" nil}}</a><header><a href="/admin/">{{.Locale.Text "admin" "administration" nil}}</a>{{if .SignedIn}}<button type="button" id="logout">{{.Locale.Text "admin" "sign_out" nil}}</button>{{end}}</header>
<div class="shell"><nav aria-label="{{.Locale.Text "admin" "navigation" nil}}"><ul>{{range .Navigation}}<li><a href="/admin/{{.ID}}" {{if eq $.Active .ID}}aria-current="page"{{end}}>{{.Title}}</a></li>{{end}}</ul></nav><main id="main" tabindex="-1"><h1>{{.Title}}</h1><noscript><p>{{.Locale.Text "admin" "enable_js" nil}}</p></noscript><p id="status" role="status" aria-live="polite" tabindex="-1"></p>{{.Content}}</main></div></body></html>`))

type home struct {
	Count  int
	Locale *localize.Localizer
}
type login struct {
	Locale   *localize.Localizer
	AuthPath string
}

var homeTemplate = template.Must(template.New("home").Parse(`{{define "content"}}<p>{{if .Count}}{{.Locale.Text "admin" "choose_section" nil}}{{else}}{{.Locale.Text "admin" "no_sections" nil}}{{end}}</p>{{end}}`))
var loginTemplate = template.Must(template.New("login").Parse(`{{define "content"}}<form data-login action="{{.AuthPath}}/login" method="post"><label for="login">{{.Locale.Text "admin" "login_label" nil}}</label><input id="login" name="login" dir="ltr" autocomplete="username" required maxlength="64"><label for="password">{{.Locale.Text "admin" "password_label" nil}}</label><input id="password" name="password" type="password" autocomplete="current-password" required maxlength="512"><button type="submit" disabled>{{.Locale.Text "admin" "sign_in" nil}}</button></form>{{end}}`))
