// SPDX-License-Identifier: MPL-2.0
package notes_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/admin"
	"github.com/AChWorks/achrix/audit"
	"github.com/AChWorks/achrix/identity"
	identityadmin "github.com/AChWorks/achrix/identity/admin"
	"github.com/AChWorks/achrix/media"
	mediaadmin "github.com/AChWorks/achrix/media/admin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Public synthetic credentials belong only to this disposable fixture.
const adminBrowserLogin = "browser-administrator"
const adminBrowserPassword = "Synthetic browser administrator password 123!"
const adminBrowserDeniedLogin = "browser-denied"
const adminBrowserViewerLogin = "browser-viewer"
const adminBrowserBootstrap achrix.Principal = "browser-fixture-bootstrap"

// Product policy is deliberately separate from Identity authentication. The
// viewer can enter both real surfaces, but cannot invoke a mutation or login
// discovery. Presentation visibility is not the domain authorization proof.
type adminBrowserPolicy struct{ administrator, viewer atomic.Value }

func (p *adminBrowserPolicy) Authorize(_ context.Context, actor achrix.Principal, capability, target string) error {
	if actor == identity.PublicPrincipal {
		if capability == identity.Authentication {
			return nil
		}
		return achrix.ErrDenied
	}
	if actor == adminBrowserBootstrap || string(actor) == p.administrator.Load().(string) {
		switch capability {
		case identity.AccountCreate, identity.AccountRead, identity.AccountLookup,
			identity.CredentialSet, identity.AccountSetEnabled, identity.SessionRevokeAll,
			audit.Append, media.Create, media.List, media.Read, media.Delete:
			return nil
		}
	}
	if string(actor) == p.viewer.Load().(string) {
		if capability == identity.AccountRead || capability == media.List && target == media.LibraryTarget {
			return nil
		}
	}
	return achrix.ErrDenied
}

type adminBrowserEndpoint struct {
	Language string `json:"language"`
	Origin   string `json:"origin"`
	SPKI     string `json:"spki"`
}
type adminBrowserImages struct {
	PNG  string `json:"png"`
	JPEG string `json:"jpeg"`
}
type adminBrowserMetadata struct {
	Images      adminBrowserImages     `json:"images"`
	Files       map[string]string      `json:"files"`
	Endpoints   []adminBrowserEndpoint `json:"endpoints"`
	Login       string                 `json:"login"`
	Password    string                 `json:"password"`
	DeniedLogin string                 `json:"deniedLogin"`
	ViewerLogin string                 `json:"viewerLogin"`
}
type adminBrowserProbe struct {
	ID            int    `json:"id"`
	Accounts      int64  `json:"accounts"`
	Audit         int64  `json:"audit"`
	ReadyAssets   int64  `json:"readyAssets"`
	DeletedAssets int64  `json:"deletedAssets"`
	Digest        string `json:"digest"`
}

// A private control-file snapshot checks durable effects independently of the
// browser UI. Fixed queries are confined to the guarded synthetic database.
// No endpoint or SQL capability is added to the product or Modules.
func adminBrowserSnapshot(pool *pgxpool.Pool, root, assets string, id int) (adminBrowserProbe, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p := adminBrowserProbe{ID: id}
	err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM identity.accounts),(SELECT count(*) FROM audit.records),(SELECT count(*) FROM media.assets WHERE state='ready'),(SELECT count(*) FROM media.assets WHERE state='deleted')").Scan(&p.Accounts, &p.Audit, &p.ReadyAssets, &p.DeletedAssets)
	if err != nil {
		return p, err
	}
	digest := sha256.New()
	for _, query := range []string{
		"SELECT row_to_json(a)::text FROM identity.accounts a ORDER BY id",
		"SELECT row_to_json(c)::text FROM identity.credentials c ORDER BY account_id",
		"SELECT row_to_json(s)::text FROM identity.sessions s ORDER BY token_hash",
		"SELECT row_to_json(r)::text FROM audit.records r ORDER BY seq",
		"SELECT row_to_json(a)::text FROM media.assets a ORDER BY id",
	} {
		rows, err := pool.Query(ctx, query)
		if err != nil {
			return p, err
		}
		for rows.Next() {
			var value string
			if err = rows.Scan(&value); err != nil {
				rows.Close()
				return p, err
			}
			_, _ = io.WriteString(digest, value+"\n")
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return p, err
		}
	}
	entries, err := os.ReadDir(assets)
	if err != nil {
		return p, err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return p, os.ErrInvalid
		}
		file, err := os.Open(filepath.Join(assets, entry.Name()))
		if err != nil {
			return p, err
		}
		_, _ = io.WriteString(digest, entry.Name()+"\n")
		_, err = io.Copy(digest, file)
		closeErr := file.Close()
		if err != nil {
			return p, err
		}
		if closeErr != nil {
			return p, closeErr
		}
	}
	p.Digest = hex.EncodeToString(digest.Sum(nil))
	data, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	path := filepath.Join(root, "probe-result.json")
	if err = os.WriteFile(path+".pending", data, 0600); err != nil {
		return p, err
	}
	return p, os.Rename(path+".pending", path)
}

// This opt-in real server is used only by the explicit browser runner. The
// runner owns the private PostgreSQL cluster and assets/control directory.
func TestAdminBrowserFixture(t *testing.T) {
	if os.Getenv("NOTES_ADMIN_BROWSER") != "1" {
		t.Skip("explicit browser runner only")
	}
	root := os.Getenv("NOTES_ADMIN_BROWSER_ROOT")
	info, err := os.Lstat(root)
	if err != nil || !filepath.IsAbs(root) || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("private browser fixture directory required")
	}
	dsn := os.Getenv("NOTES_ADMIN_TEST_DSN")
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil || dsn == "" || c.ConnConfig.Database != "achrix_admin_test" || c.ConnConfig.Host != filepath.Join(root, "socket") || len(c.ConnConfig.Fallbacks) != 0 {
		t.Fatal("owned Unix-socket achrix_admin_test database required")
	}
	c.MaxConns = 2
	c.ConnConfig.ConnectTimeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		t.Fatal("browser fixture database unavailable")
	}
	defer pool.Close()
	var actual, encoding string
	if err = pool.QueryRow(ctx, "SELECT current_database(),current_setting('server_encoding')").Scan(&actual, &encoding); err != nil || actual != "achrix_admin_test" || encoding != "UTF8" {
		t.Fatal("browser fixture database identity mismatch")
	}
	if _, err = pool.Exec(ctx, "DROP SCHEMA IF EXISTS identity CASCADE; DROP SCHEMA IF EXISTS audit CASCADE; DROP SCHEMA IF EXISTS media CASCADE"); err != nil {
		t.Fatal("owned fixture schema reset failed")
	}
	for _, migrate := range []func(context.Context, string) error{audit.Migrate, identity.Migrate, media.Migrate} {
		if err = migrate(ctx, dsn); err != nil {
			t.Fatal(err)
		}
	}
	assets := filepath.Join(root, "assets")
	if err = os.Mkdir(assets, 0700); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	am, err := audit.NewPostgres(dsn, audit.Config{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	im, err := identity.NewPostgres(dsn, identity.Config{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	mm, err := media.NewPostgres(dsn, media.Config{StorageRoot: assets, AllowedMIMEs: append(media.CommonMIMEs(), "image/svg+xml")}, logger)
	if err != nil {
		t.Fatal(err)
	}
	policy := &adminBrowserPolicy{}
	policy.administrator.Store("")
	policy.viewer.Store("")
	app, err := achrix.New(achrix.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 3 * time.Second, Logger: logger}, policy, im, am, mm)
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	as, err := audit.NewService(app, am)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := identity.NewService(app, im, as)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := media.NewService(app, mm)
	if err != nil {
		t.Fatal(err)
	}
	account, err := ids.CreateAccount(ctx, adminBrowserBootstrap, adminBrowserLogin, adminBrowserPassword)
	if err != nil {
		t.Fatal(err)
	}
	policy.administrator.Store(account.ID)
	account, err = ids.CreateAccount(ctx, adminBrowserBootstrap, adminBrowserViewerLogin, adminBrowserPassword)
	if err != nil {
		t.Fatal(err)
	}
	policy.viewer.Store(account.ID)
	if _, err = ids.CreateAccount(ctx, adminBrowserBootstrap, adminBrowserDeniedLogin, adminBrowserPassword); err != nil {
		t.Fatal(err)
	}
	metadata := adminBrowserMetadata{Login: adminBrowserLogin, Password: adminBrowserPassword, DeniedLogin: adminBrowserDeniedLogin, ViewerLogin: adminBrowserViewerLogin}
	image := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	image.SetNRGBA(0, 0, color.NRGBA{R: 23, G: 117, B: 194, A: 255})
	image.SetNRGBA(1, 0, color.NRGBA{R: 194, G: 117, B: 23, A: 255})
	var pngData, jpegData bytes.Buffer
	if err = png.Encode(&pngData, image); err != nil {
		t.Fatal(err)
	}
	if err = jpeg.Encode(&jpegData, image, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	metadata.Images = adminBrowserImages{base64.StdEncoding.EncodeToString(pngData.Bytes()), base64.StdEncoding.EncodeToString(jpegData.Bytes())}
	metadata.Files = make(map[string]string)
	for filename, data := range mediaDocumentBytes(t) {
		metadata.Files[filename] = base64.StdEncoding.EncodeToString(data)
	}
	var certificates []byte
	for _, language := range []string{"en", "fa"} {
		server := httptest.NewUnstartedServer(http.NotFoundHandler())
		origin := "https://" + server.Listener.Addr().String()
		web, err := identity.NewWeb(ids, origin)
		if err != nil {
			t.Fatal(err)
		}
		auth, err := identityadmin.Authenticator(web)
		if err != nil {
			t.Fatal(err)
		}
		is, err := identityadmin.New(ids)
		if err != nil {
			t.Fatal(err)
		}
		mss, err := mediaadmin.New(ms)
		if err != nil {
			t.Fatal(err)
		}
		shell, err := admin.New(admin.Config{Origin: origin, AuthPath: "/auth", Language: language}, auth, app, is, mss)
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		mux.Handle("/auth/", http.StripPrefix("/auth", web.Handler()))
		mux.Handle("/admin", shell.Handler())
		mux.Handle("/admin/", shell.Handler())
		server.Config.Handler = mux
		admin.ConfigureServer(server.Config)
		server.StartTLS()
		t.Cleanup(server.Close)
		certificates = append(certificates, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})...)
		fingerprint := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
		metadata.Endpoints = append(metadata.Endpoints, adminBrowserEndpoint{language, server.URL, base64.StdEncoding.EncodeToString(fingerprint[:])})
	}
	if err = os.WriteFile(filepath.Join(root, "fixture-ca.pem"), certificates, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "fixture.json")
	if err = os.WriteFile(path+".pending", data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path+".pending", path); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(180 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	lastProbe := 0
	for {
		select {
		case <-timer.C:
			t.Fatal("browser fixture deadline exceeded")
		case <-ticker.C:
			if command, err := os.ReadFile(filepath.Join(root, "probe.json")); err == nil {
				var request struct {
					ID int `json:"id"`
				}
				if err = json.Unmarshal(command, &request); err != nil || request.ID < 1 || request.ID > 100 {
					t.Fatal("invalid private probe command")
				}
				if request.ID > lastProbe {
					if _, err = adminBrowserSnapshot(pool, root, assets, request.ID); err != nil {
						t.Fatal("private durable-state probe failed", err)
					}
					lastProbe = request.ID
				}
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			result, err := os.ReadFile(filepath.Join(root, "done"))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(result)) != "PASS" {
				t.Fatal("browser proof did not pass")
			}
			t.Log("real HTTPS Identity/Media/Admin browser fixture completed")
			return
		}
	}
}
