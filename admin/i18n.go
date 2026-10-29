// SPDX-License-Identifier: MPL-2.0
package admin

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/AChWorks/achrix/admin/localize"
)

//go:embed locales/*.json
var catalogs embed.FS

// Catalogs returns the shell-owned English/Persian message resources. Products
// may compose them with their own sources without constructing an Admin shell.
func Catalogs() fs.FS {
	source, _ := fs.Sub(catalogs, "locales")
	return source
}

// Locales returns a detached inventory of the instance's loaded UI languages.
// The product decides which choices to present and where preferences are kept.
func (s *Shell) Locales() []localize.Locale { return s.catalog.Locales() }

func (s *Shell) configureCatalogs() error {
	sources := []fs.FS{Catalogs()}
	for _, surface := range s.surfaces {
		if !nilInterface(surface.Catalogs) {
			sources = append(sources, surface.Catalogs)
		}
	}
	for _, source := range s.config.Catalogs {
		if nilInterface(source) {
			return fmt.Errorf("%w: nil catalog source", ErrConfiguration)
		}
		sources = append(sources, source)
	}
	catalog, err := localize.Load(sources...)
	if err != nil {
		return fmt.Errorf("%w: catalogs: %w", ErrConfiguration, err)
	}
	if _, err = catalog.For(s.config.Language); err != nil {
		return fmt.Errorf("%w: default locale: %w", ErrConfiguration, err)
	}
	s.catalog = catalog
	// Keep the immutable in-memory snapshot, never the filesystem inputs.
	s.config.Catalogs = nil
	for i := range s.surfaces {
		s.surfaces[i].Catalogs = nil
	}
	s.browser = make(map[string]map[string]string)
	for _, metadata := range catalog.Locales() {
		locale, localeErr := catalog.For(metadata.Tag)
		if localeErr != nil {
			return fmt.Errorf("%w: locale inventory: %w", ErrConfiguration, localeErr)
		}
		for _, surface := range s.surfaces {
			title, titleErr := locale.Text(surface.Title.Domain, surface.Title.Key, nil)
			if titleErr != nil || title == "" || len(title) > 512 {
				return fmt.Errorf("%w: surface %s title %s/%s", ErrConfiguration, surface.ID, surface.Title.Domain, surface.Title.Key)
			}
		}
		messages, messageErr := locale.Messages("admin")
		if messageErr != nil {
			return fmt.Errorf("%w: shell messages: %w", ErrConfiguration, messageErr)
		}
		variants := map[string]string{"": "admin", "login": "admin"}
		for _, surface := range s.surfaces {
			variants[surface.ID] = surface.Title.Domain
		}
		s.browser[metadata.Tag] = make(map[string]string, len(variants))
		byDomain := make(map[string]string)
		for active, domain := range variants {
			if data, exists := byDomain[domain]; exists {
				s.browser[metadata.Tag][active] = data
				continue
			}
			domains := map[string]map[string]localize.Message{"admin": messages}
			if domain != "admin" {
				domainMessages, domainErr := locale.Messages(domain)
				if domainErr != nil {
					return fmt.Errorf("%w: surface %s messages: %w", ErrConfiguration, active, domainErr)
				}
				domains[domain] = domainMessages
			}
			data, encodeErr := json.Marshal(struct {
				Locale  string                                 `json:"locale"`
				Domains map[string]map[string]localize.Message `json:"domains"`
			}{metadata.Tag, domains})
			if encodeErr != nil || len(data) > 64<<10 {
				return fmt.Errorf("%w: browser catalog exceeds bound for %s/%s", ErrConfiguration, metadata.Tag, active)
			}
			byDomain[domain] = string(data)
			s.browser[metadata.Tag][active] = byDomain[domain]
		}
	}
	return nil
}

func (s *Shell) requestLocale(r *http.Request) (*localize.Localizer, error) {
	language := s.config.Language
	if s.config.SelectLanguage != nil {
		if selected := s.config.SelectLanguage(r); selected != "" {
			language = selected
		}
	}
	return s.catalog.For(language)
}

func assetPath(path string) (name, kind string, ok bool) {
	switch path {
	case "/admin/assets/admin.css":
		return "assets/admin.css", "text/css; charset=utf-8", true
	case "/admin/assets/admin.js":
		return "assets/admin.js", "text/javascript; charset=utf-8", true
	case "/admin/assets/i18n.js":
		return "assets/i18n.js", "text/javascript; charset=utf-8", true
	default:
		return "", "", false
	}
}
