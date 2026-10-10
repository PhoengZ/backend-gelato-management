// Demo provisions only private local fixtures. It never changes public signup.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const demoDatabase = "auth_demo"

var roles = []string{"CUSTOMER", "STAFF", "MANAGER"}
var namespacePattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type config struct{ authURL, databaseURL, stateDir string }
type fixture struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}
type credentials struct {
	SchemaVersion int      `json:"schema_version"`
	Namespace     string   `json:"namespace"`
	Customer      *fixture `json:"customer"`
	Staff         *fixture `json:"staff"`
	Manager       *fixture `json:"manager"`
}
type user struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}
type session struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	User        user   `json:"user"`
}
type verification struct {
	Status             string `json:"status"`
	RecordedAt         string `json:"recorded_at"`
	Database           string `json:"database"`
	RealAuthLogin      bool   `json:"real_auth_login"`
	RealAuthMe         bool   `json:"real_auth_me"`
	LocallyMintedJWT   bool   `json:"locally_minted_jwt"`
	VerifyOnly         bool   `json:"verify_only"`
	CredentialsPrivate bool   `json:"credentials_private"`
	Accounts           []user `json:"accounts"`
}

func main() {
	verifyOnly := flag.Bool("verify-only", false, "Verify existing fixture logins without signup or role changes")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Auth demo accepts environment configuration and --verify-only only")
		os.Exit(1)
	}
	cfg, err := loadConfig()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err = run(ctx, cfg, os.Stdout, *verifyOnly)
	}
	if err != nil {
		// Errors from SQL/HTTP bodies and connection strings are never printed.
		fmt.Fprintln(os.Stderr, "Auth demo:", err)
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	cfg := config{authURL: os.Getenv("AUTH_DEMO_URL"), databaseURL: os.Getenv("AUTH_DEMO_DATABASE_URL"), stateDir: os.Getenv("AUTH_DEMO_STATE_DIR")}
	if cfg.authURL == "" {
		cfg.authURL = "http://127.0.0.1:17011"
	}
	if cfg.stateDir == "" {
		cfg.stateDir = filepath.Join("demo", ".local")
	}
	if err := validateConfig(cfg); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func validateConfig(cfg config) error {
	u, err := url.Parse(cfg.authURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("AUTH_DEMO_URL must be an HTTP(S) origin without credentials")
	}
	address := net.ParseIP(u.Hostname())
	if !strings.EqualFold(u.Hostname(), "localhost") && (address == nil || !address.IsLoopback()) {
		return errors.New("AUTH_DEMO_URL must use a loopback host for local fixture provisioning")
	}
	u, err = url.Parse(cfg.databaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" || u.Path != "/"+demoDatabase || u.Fragment != "" {
		return errors.New("AUTH_DEMO_DATABASE_URL must target the dedicated auth_demo PostgreSQL database")
	}
	db, err := pgxpool.ParseConfig(cfg.databaseURL)
	if err != nil || db.ConnConfig.Database != demoDatabase {
		return errors.New("AUTH_DEMO_DATABASE_URL must target the dedicated auth_demo PostgreSQL database")
	}
	return nil
}

func run(ctx context.Context, cfg config, out io.Writer, verifyOnly bool) error {
	pool, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return errors.New("could not configure the demo database")
	}
	defer pool.Close()
	var database string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&database); err != nil {
		return errors.New("could not connect to the demo database")
	}
	if database != demoDatabase {
		return errors.New("refusing a database other than auth_demo")
	}
	state, err := loadState(cfg.stateDir, verifyOnly)
	if err != nil {
		return err
	}
	persist := func() error { return writePrivate(cfg.stateDir, "credentials.json", state) }
	// Persist newly generated credentials before any signup so interrupted runs recover.
	if err := persist(); err != nil {
		return err
	}
	api := &authClient{origin: strings.TrimRight(cfg.authURL, "/"), client: &http.Client{
		Timeout:       10 * time.Second,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	result, err := prepare(ctx, state, api, postgresStore{pool}, persist, out, verifyOnly)
	if err != nil {
		return err
	}
	if err := writePrivate(cfg.stateDir, "verification.json", result); err != nil {
		return err
	}
	fmt.Fprintln(out, "PASS: real Auth login and /me verified for Customer, Staff and Manager; credentials saved privately")
	return nil
}

func fixtures(state *credentials) []*fixture {
	return []*fixture{state.Customer, state.Staff, state.Manager}
}

func newState() (*credentials, error) {
	identity := make([]byte, 16)
	if _, err := rand.Read(identity); err != nil {
		return nil, errors.New("could not generate fixture identities")
	}
	state := &credentials{SchemaVersion: 1, Namespace: hex.EncodeToString(identity)}
	accounts := []*fixture{}
	for _, role := range roles {
		password := make([]byte, 32)
		if _, err := rand.Read(password); err != nil {
			return nil, errors.New("could not generate fixture credentials")
		}
		accounts = append(accounts, &fixture{Email: fixtureEmail(state.Namespace, role), Password: hex.EncodeToString(password), DisplayName: "Auth Demo " + role, Role: role})
	}
	state.Customer, state.Staff, state.Manager = accounts[0], accounts[1], accounts[2]
	return state, nil
}

func fixtureEmail(namespace, role string) string {
	return "authdemo-" + namespace + "-" + strings.ToLower(role) + "@gelatoflow.test"
}

func validateState(state *credentials) error {
	if state == nil || state.SchemaVersion != 1 || !namespacePattern.MatchString(state.Namespace) {
		return errors.New("invalid private fixture state")
	}
	seen := map[string]bool{}
	for i, account := range fixtures(state) {
		if account == nil || account.Role != roles[i] || account.Email != fixtureEmail(state.Namespace, roles[i]) || account.DisplayName != "Auth Demo "+roles[i] || len(account.Password) != 64 {
			return errors.New("fixture state does not match its dedicated demo identities")
		}
		if _, err := hex.DecodeString(account.Password); err != nil {
			return errors.New("invalid private fixture credentials")
		}
		if account.ID != "" {
			id, err := uuid.Parse(account.ID)
			if err != nil || id == uuid.Nil || seen[id.String()] {
				return errors.New("invalid or repeated fixture account ID")
			}
			seen[id.String()] = true
		}
	}
	return nil
}

func loadState(dir string, verifyOnly bool) (*credentials, error) {
	if err := privateDirectory(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "credentials.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if verifyOnly {
			return nil, errors.New("no fixtures yet; run the demo seed without --verify-only first")
		}
		return newState()
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return nil, errors.New("private fixture state must be a regular local file")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, errors.New("could not protect private fixture state")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("could not read private fixture state")
	}
	defer file.Close()
	var state credentials
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return nil, errors.New("invalid private fixture state")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("invalid private fixture state")
	}
	if err := validateState(&state); err != nil {
		return nil, err
	}
	return &state, nil
}

func privateDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("could not create private demo state directory")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("demo state must be a local directory, not a symbolic link")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return errors.New("could not protect private demo directory")
	}
	return nil
}

func writePrivate(dir, name string, value any) error {
	if err := privateDirectory(dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("could not encode demo state")
	}
	file, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return errors.New("could not save private demo state")
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return errors.New("could not save private demo state")
	}
	if err := file.Close(); err != nil {
		return errors.New("could not save private demo state")
	}
	if err := os.Rename(temporary, filepath.Join(dir, name)); err != nil {
		return errors.New("could not save private demo state")
	}
	return nil
}

type authClient struct {
	origin string
	client *http.Client
}

func (a *authClient) request(ctx context.Context, method, endpoint, token string, body any, response any) (int, error) {
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, errors.New("could not encode the Auth demo request")
		}
		input = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.origin+"/api/v1/auth/"+endpoint, input)
	if err != nil {
		return 0, errors.New("could not prepare the Auth demo request")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := a.client.Do(req)
	if err != nil {
		return 0, errors.New("Auth request failed; check the local service and configuration")
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		// Never echo response bodies: proxies and misconfigured servers can reflect credentials.
		return res.StatusCode, nil
	}
	if response != nil {
		if err := json.NewDecoder(io.LimitReader(res.Body, 32<<10)).Decode(response); err != nil {
			return 0, errors.New("Auth returned an invalid demo response")
		}
	}
	return res.StatusCode, nil
}

func (a *authClient) authenticate(ctx context.Context, account *fixture) (session, error) {
	var login session
	status, err := a.request(ctx, http.MethodPost, "login", "", map[string]string{"email": account.Email, "password": account.Password}, &login)
	if err != nil || status != http.StatusOK {
		return session{}, errors.New("fixture login failed; private credentials were not changed")
	}
	if login.AccessToken == "" || login.TokenType != "Bearer" || login.ExpiresIn < 1 || login.ExpiresIn > 3600 || !matchesFixture(login.User, account) {
		return session{}, errors.New("Auth login did not return the expected active fixture identity")
	}
	var me user
	status, err = a.request(ctx, http.MethodGet, "me", login.AccessToken, nil, &me)
	if err != nil || status != http.StatusOK || me != login.User {
		return session{}, errors.New("Auth /me did not confirm the fixture login identity")
	}
	return login, nil
}

func matchesFixture(u user, account *fixture) bool {
	id, err := uuid.Parse(u.ID)
	return err == nil && id != uuid.Nil && (account.ID == "" || account.ID == id.String()) && u.Email == account.Email && u.DisplayName == account.DisplayName && u.Status == "ACTIVE" && (u.Role == "CUSTOMER" || u.Role == account.Role)
}

type roleStore interface {
	find(context.Context, string) (user, error)
	promote(context.Context, user, string) error
}
type postgresStore struct{ pool *pgxpool.Pool }

func (s postgresStore) find(ctx context.Context, id string) (user, error) {
	var u user
	err := s.pool.QueryRow(ctx, "SELECT id::text,email,display_name,role,status FROM users WHERE id=$1::uuid", id).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Status)
	if err != nil {
		return user{}, errors.New("could not confirm the fixture in the dedicated demo database")
	}
	return u, nil
}

func (s postgresStore) promote(ctx context.Context, before user, role string) error {
	var id string
	err := s.pool.QueryRow(ctx, `UPDATE users SET role=$1,updated_at=NOW()
		WHERE id=$2::uuid AND email=$3 AND display_name=$4 AND status='ACTIVE' AND role=$5
		RETURNING id::text`, role, before.ID, before.Email, before.DisplayName, before.Role).Scan(&id)
	if err != nil || id != before.ID {
		return errors.New("trusted fixture role update did not match the confirmed demo account")
	}
	return nil
}

func prepare(ctx context.Context, state *credentials, api *authClient, store roleStore, persist func() error, out io.Writer, verifyOnly bool) (verification, error) {
	if err := validateState(state); err != nil {
		return verification{}, err
	}
	result := verification{Status: "PASS", RecordedAt: time.Now().UTC().Format(time.RFC3339), Database: demoDatabase, VerifyOnly: verifyOnly, CredentialsPrivate: true}
	for _, account := range fixtures(state) {
		if !verifyOnly {
			var created user
			status, err := api.request(ctx, http.MethodPost, "signup", "", map[string]string{"email": account.Email, "password": account.Password, "display_name": account.DisplayName}, &created)
			if err != nil || (status != http.StatusCreated && status != http.StatusConflict) {
				return verification{}, errors.New("fixture signup failed; no role was granted")
			}
			if status == http.StatusCreated && (!matchesFixture(created, account) || created.Role != "CUSTOMER") {
				return verification{}, errors.New("public signup did not return the expected Customer fixture")
			}
			if status == http.StatusCreated {
				account.ID = created.ID
				if err := persist(); err != nil {
					return verification{}, err
				}
			}
		}
		login, err := api.authenticate(ctx, account)
		if err != nil {
			return verification{}, err
		}
		dbUser, err := store.find(ctx, login.User.ID)
		if err != nil || dbUser != login.User {
			return verification{}, errors.New("Auth and the dedicated demo database identify different accounts; no role was granted")
		}
		account.ID = login.User.ID
		if err := persist(); err != nil {
			return verification{}, err
		}
		if dbUser.Role != account.Role {
			if verifyOnly {
				return verification{}, errors.New("fixture role is not prepared; seed without --verify-only first")
			}
			if dbUser.Role != "CUSTOMER" || account.Role == "CUSTOMER" {
				return verification{}, errors.New("refusing an unexpected fixture role transition")
			}
			if err := store.promote(ctx, dbUser, account.Role); err != nil {
				return verification{}, err
			}
			login, err = api.authenticate(ctx, account)
			if err != nil || login.User.Role != account.Role {
				return verification{}, errors.New("fresh Auth login did not confirm the trusted fixture role")
			}
		}
		account.Status, account.AccessToken, account.ExpiresIn = login.User.Status, login.AccessToken, login.ExpiresIn
		if err := persist(); err != nil {
			return verification{}, err
		}
		result.Accounts = append(result.Accounts, login.User)
		fmt.Fprintf(out, "%s: real login / me PASS; id=%s email=%s role=%s status=%s; JWT hidden\n", account.Role, account.ID, account.Email, login.User.Role, login.User.Status)
	}
	result.RealAuthLogin, result.RealAuthMe = true, true
	return result, nil
}
