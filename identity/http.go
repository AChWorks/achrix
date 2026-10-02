// SPDX-License-Identifier: MPL-2.0
package identity

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/AChWorks/achrix"
)

const CookieName = "__Host-AChrix-Session"

// Web owns a deliberately same-origin HTTPS browser boundary. It does not trust
// Forwarded/X-Forwarded-* or implement CORS. A product must terminate TLS here or
// compose a separately reviewed trusted proxy boundary before invoking this API.
type Web struct {
	service *Service
	origin  string
	host    string
	slots   chan struct{}
	limits  loginLimits
}

func NewWeb(service *Service, origin string) (*Web, error) {
	if service == nil {
		return nil, ErrConfiguration
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.String() != origin {
		return nil, ErrConfiguration
	}
	return &Web{service: service, origin: origin, host: u.Host, slots: make(chan struct{}, 16)}, nil
}

// Handler serves /login, /csrf, /rotate, /logout and /password relative to its
// mount. All browser responses are non-cacheable, with no reflected diagnostics.
func (w *Web) Handler() http.Handler { return http.HandlerFunc(w.serve) }
func (w *Web) secureRequest(r *http.Request) bool {
	if r.TLS == nil || r.Host != w.host {
		return false
	}
	if r.Header.Get("Origin") != "" {
		return len(r.Header.Values("Origin")) == 1 && r.Header.Get("Origin") == w.origin
	}
	referer := r.Header.Get("Referer")
	u, err := url.Parse(referer)
	return err == nil && u != nil && u.Scheme == "https" && u.Host == w.host && u.User == nil && u.Opaque == "" && len(r.Header.Values("Referer")) == 1
}
func cookieToken(r *http.Request) (string, error) {
	var found string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == CookieName {
			found = cookie.Value
			count++
		}
	}
	if count != 1 {
		return "", ErrAuthentication
	}
	if _, err := tokenHash(found); err != nil {
		return "", err
	}
	return found, nil
}
func (w *Web) setCookie(writer http.ResponseWriter, s Session) {
	// Nonpersistent browser cookie; absolute expiry is enforced by PostgreSQL.
	http.SetCookie(writer, &http.Cookie{Name: CookieName, Value: s.Token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}
func (w *Web) clearCookie(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{Name: CookieName, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

// AuthenticateRequest validates cookie authentication and, for mutations, the
// synchronizer token and exact Origin/Referer. It returns no authority: the owning
// product Application operation must still authorize this principal.
func (w *Web) AuthenticateRequest(r *http.Request, mutation bool) (achrix.Principal, error) {
	if r.TLS == nil || r.Host != w.host {
		return "", ErrAuthentication
	}
	value, err := cookieToken(r)
	if err != nil {
		return "", err
	}
	if mutation {
		if !w.secureRequest(r) || len(r.Header.Values("X-CSRF-Token")) != 1 {
			return "", ErrAuthentication
		}
		return w.service.ValidateCSRF(r.Context(), value, r.Header.Get("X-CSRF-Token"))
	}
	return w.service.Authenticate(r.Context(), value)
}
func (w *Web) serve(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if !w.secureRequest(r) || r.URL.RawQuery != "" {
		webError(writer, ErrAuthentication)
		return
	}
	// Every action uses a non-simple header. This also protects initial login where
	// there is deliberately no attacker-created anonymous persistent session.
	if r.Header.Get("X-Identity-Request") != "1" || len(r.Header.Values("X-Identity-Request")) != 1 {
		webError(writer, ErrAuthentication)
		return
	}
	select {
	case w.slots <- struct{}{}:
		defer func() { <-w.slots }()
	default:
		webError(writer, ErrLimited)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if r.URL.Path == "/csrf" {
		if r.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		value, err := cookieToken(r)
		if err != nil {
			webError(writer, err)
			return
		}
		csrf, err := w.service.RefreshCSRF(ctx, value)
		if err != nil {
			webError(writer, err)
			return
		}
		_ = json.NewEncoder(writer).Encode(struct {
			CSRF string `json:"csrf"`
		}{csrf})
		return
	}
	if r.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/login":
		// The source address is the socket peer, never an attacker-provided header.
		// The fixed-size table plus global budget also bounds distributed/cardinality
		// abuse. Collisions share a conservative budget, they never evade a limit.
		if !w.limits.allow(r.RemoteAddr, w.service.module.now()) {
			webError(writer, ErrLimited)
			return
		}
		var body struct {
			Login    string `json:"login"`
			Password string `json:"password"`
		}
		if err := decodeWeb(writer, r, &body); err != nil {
			webError(writer, err)
			return
		}
		previous, _ := cookieToken(r)
		session, err := w.service.Login(ctx, body.Login, body.Password, previous)
		if err != nil {
			webError(writer, err)
			return
		}
		w.setCookie(writer, session)
		webSession(writer, session)
	case "/rotate", "/logout", "/password":
		_, err := w.AuthenticateRequest(r, true)
		if err != nil {
			webError(writer, err)
			return
		}
		value, _ := cookieToken(r)
		switch r.URL.Path {
		case "/rotate":
			session, err := w.service.Rotate(ctx, value)
			if err != nil {
				webError(writer, err)
				return
			}
			w.setCookie(writer, session)
			webSession(writer, session)
		case "/logout":
			if err = w.service.Logout(ctx, value); err != nil {
				webError(writer, err)
				return
			}
			w.clearCookie(writer)
			writer.WriteHeader(http.StatusNoContent)
		case "/password":
			var body struct {
				Current string `json:"current"`
				Next    string `json:"next"`
			}
			if err = decodeWeb(writer, r, &body); err != nil {
				webError(writer, err)
				return
			}
			if err = w.service.ChangePassword(ctx, value, body.Current, body.Next); err != nil {
				webError(writer, err)
				return
			}
			w.clearCookie(writer)
			writer.WriteHeader(http.StatusNoContent)
		}
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}
func decodeWeb(writer http.ResponseWriter, r *http.Request, target any) error {
	if r.Header.Get("Content-Type") != "application/json" {
		return ErrInvalid
	}
	r.Body = http.MaxBytesReader(writer, r.Body, 2048)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrInvalid
	}
	return nil
}
func webSession(writer http.ResponseWriter, s Session) {
	_ = json.NewEncoder(writer).Encode(struct {
		Principal achrix.Principal `json:"principal"`
		CSRF      string           `json:"csrf"`
		ExpiresAt time.Time        `json:"expires_at"`
	}{s.Principal, s.CSRF, s.ExpiresAt})
}
func webError(writer http.ResponseWriter, err error) {
	code, status := "authentication_failed", http.StatusUnauthorized
	switch {
	case errors.Is(err, ErrLimited):
		code, status = "authentication_limited", http.StatusTooManyRequests
	case errors.Is(err, ErrInvalid):
		code, status = "invalid_input", http.StatusBadRequest
	case errors.Is(err, ErrUnavailable) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		code, status = "authentication_unavailable", http.StatusServiceUnavailable
	case errors.Is(err, ErrConflict):
		code, status = "authentication_conflict", http.StatusConflict
	}
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(struct {
		Code string `json:"code"`
	}{code})
}

// Fixed-cardinality, no-queue public login budget: 60 total/minute and 10 per
// peer bucket/minute, independent of claimed username. No map grows with input.
// This development baseline bounds resource use; a real deployment must validate
// address/proxy topology and tune policy from measured legitimate requirements.
type loginLimits struct {
	mu     sync.Mutex
	window time.Time
	global uint16
	peers  [256]uint8
}

func (l *loginLimits) allow(remote string, now time.Time) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	hash := sha256.Sum256(ip.To16())
	index := hash[0]
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.window.IsZero() || now.Sub(l.window) >= time.Minute {
		l.window = now
		l.global = 0
		l.peers = [256]uint8{}
	}
	if now.Before(l.window) || l.global >= 60 || l.peers[index] >= 10 {
		return false
	}
	l.global++
	l.peers[index]++
	return true
}
