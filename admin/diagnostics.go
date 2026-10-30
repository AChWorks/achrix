// SPDX-License-Identifier: MPL-2.0
package admin

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/admin/localize"
)

const diagnosticWindow = time.Second

type diagnosticKey struct {
	operation, phase, reason, causeType string
	fingerprint                         [32]byte
	truncated                           bool
	originalLength                      int
}
type diagnosticSample struct{ count, sequence uint64 }
type diagnosticWindowState struct {
	mu                 sync.Mutex
	generation         uint64
	timer              *time.Timer
	entries            map[diagnosticKey]diagnosticSample
	regular, uncertain int
	overflow           uint64
	nextSequence       uint64
}

// Diagnose reports an adapter-owned failure at its original boundary. The shell
// supplies the current request context. Pass compiled operation/phase names,
// never user input, bodies, account identifiers or resource paths as names.
// The zero Request safely does nothing, which also supports standalone surfaces.
func (r Request) Diagnose(operation, phase string, cause error) {
	if r.diagnose != nil && cause != nil {
		r.diagnose(operation, phase, cause)
	}
}

func diagnosticName(value string) string {
	if value == "" || len(value) > 64 {
		return "adapter"
	}
	for _, c := range value {
		if c != '_' && c != '-' && c != '.' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return "adapter"
		}
	}
	return value
}

func diagnosticReason(ctx context.Context, err error) string {
	var network net.Error
	switch {
	case ctx.Err() != nil && errors.Is(err, ctx.Err()):
		return "caller_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "dependency_deadline"
	case errors.Is(err, context.Canceled):
		return "dependency_canceled"
	case errors.Is(err, http.ErrNotSupported):
		return "deadline_unsupported"
	case errors.Is(err, io.ErrShortWrite):
		return "short_write"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	case errors.Is(err, io.ErrClosedPipe):
		return "closed_pipe"
	case errors.Is(err, localize.ErrLocale):
		return "unsupported_locale"
	case errors.Is(err, localize.ErrMessage):
		return "missing_message"
	case errors.Is(err, localize.ErrParameters):
		return "invalid_message_parameters"
	case errors.Is(err, localize.ErrLimit), errors.Is(err, errPageLimit):
		return "output_limit"
	case errors.Is(err, localize.ErrCatalog):
		return "invalid_catalog"
	case errors.Is(err, ErrConfiguration):
		return "configuration"
	case errors.Is(err, achrix.ErrAuthorizationUnavailable):
		return "policy_unavailable"
	case errors.As(err, &network) && network.Timeout():
		return "transport_timeout"
	default:
		return "dependency_failure"
	}
}

func saturatingIncrement(value uint64) uint64 {
	if value != ^uint64(0) {
		return value + 1
	}
	return value
}

func (s *Shell) diagnose(ctx context.Context, operation, phase string, err error) {
	if err == nil {
		return
	}
	operation, phase = diagnosticName(operation), diagnosticName(phase)
	// The optional protected sink sees each cause before ordinary suppression.
	// Its owner chooses provider-aware redaction, repetition and retention.
	if s.config.Diagnostic != nil {
		s.config.Diagnostic(ctx, operation, phase, err)
	}
	// A bounded private fingerprint distinguishes two arbitrary provider causes
	// without publishing or retaining their text. Typed cause/phase remains useful
	// in ordinary logs; the explicitly configured protected hook owns full detail.
	message := err.Error()
	originalLength := len(message)
	truncated := originalLength > 4096
	if truncated {
		message = message[:4096]
	}
	causeType := reflect.TypeOf(err).String()
	if len(causeType) > 128 {
		causeType = causeType[:128]
	}
	key := diagnosticKey{operation, phase, diagnosticReason(ctx, err), causeType, sha256.Sum256([]byte(message)), truncated, originalLength}
	uncertain := phase == "mutation_outcome_unknown" || phase == "commit_unknown" || phase == "response_write"
	d := &s.diagnostics
	d.mu.Lock()
	if d.entries == nil {
		d.entries = make(map[diagnosticKey]diagnosticSample, 64)
	}
	if d.timer == nil {
		d.generation++
		generation := d.generation
		d.timer = time.AfterFunc(diagnosticWindow, func() { s.flushDiagnostics(generation, false) })
	}
	sample, exists := d.entries[key]
	admit := !exists && ((!uncertain && d.regular < 56) || (uncertain && d.uncertain < 8))
	if exists {
		sample.count = saturatingIncrement(sample.count)
		d.entries[key] = sample
	} else if admit {
		d.nextSequence = saturatingIncrement(d.nextSequence)
		sample.sequence = d.nextSequence
		d.entries[key] = sample
		if uncertain {
			d.uncertain++
		} else {
			d.regular++
		}
	} else {
		d.overflow = saturatingIncrement(d.overflow)
	}
	d.mu.Unlock()
	if !admit {
		return
	}
	level := slog.LevelError
	if key.reason == "caller_canceled" {
		level = slog.LevelDebug
	}
	s.config.Logger.Log(ctx, level, "admin operation failed", "component", "achrix.admin", "operation", operation, "phase", phase, "reason", key.reason, "cause_type", causeType, "cause_sample_truncated", truncated, "diagnostic_sequence", sample.sequence)
}

// FlushDiagnostics emits any pending bounded suppression evidence. A product
// may call this after ingress shutdown; the ordinary timer also flushes when
// traffic stops. It does not wait for in-flight requests or stop the shell.
func (s *Shell) FlushDiagnostics() { s.flushDiagnostics(0, true) }

func (s *Shell) flushDiagnostics(generation uint64, final bool) {
	d := &s.diagnostics
	d.mu.Lock()
	if !final && generation != d.generation {
		d.mu.Unlock()
		return
	}
	entries, overflow := d.entries, d.overflow
	if d.timer != nil {
		d.timer.Stop()
	}
	d.generation++ // An already-fired old timer cannot clear a later window.
	d.timer, d.entries, d.overflow = nil, nil, 0
	d.regular, d.uncertain = 0, 0
	d.mu.Unlock()
	// These records summarize many requests, never the first request's context.
	for key, sample := range entries {
		if sample.count != 0 {
			level := slog.LevelError
			if key.reason == "caller_canceled" {
				level = slog.LevelDebug
			}
			s.config.Logger.Log(context.Background(), level, "admin repeated failure summary", "component", "achrix.admin", "operation", key.operation, "phase", key.phase, "reason", key.reason, "cause_type", key.causeType, "suppressed_records", sample.count, "diagnostic_sequence", sample.sequence, "cause_sample_truncated", key.truncated, "diagnostic_summary", true)
		}
	}
	if overflow != 0 {
		s.config.Logger.Error("admin diagnostic capacity exceeded", "component", "achrix.admin", "operation", "diagnostics", "phase", "suppression", "overflow_records", overflow, "sampled", true, "diagnostic_summary", true)
	}
}
