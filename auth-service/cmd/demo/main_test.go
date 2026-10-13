package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type demoStoredAccount struct {
	identity user
	password string
}

// HTTP and the role store share persisted users, while issued sessions keep the
// identity at login time. Promotion therefore needs a fresh login to change it.
type demoAuthFake struct {
	mu                        sync.Mutex
	accounts                  map[string]demoStoredAccount
	tokens                    map[string]user
	signupBodies              []map[string]string
	loginCalls, meCalls       int
	loginOverride, meOverride func(user) user
	issuedTokens              []string
}

func newDemoAuthFake(t *testing.T) (*demoAuthFake, *authClient) {
	t.Helper()
	fake := &demoAuthFake{accounts: make(map[string]demoStoredAccount), tokens: make(map[string]user)}
	server := httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(server.Close)
	return fake, &authClient{origin: server.URL, client: &http.Client{Timeout: time.Second}}
}

func (f *demoAuthFake) seed(state *credentials, prepared bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, account := range fixtures(state) {
		role := "CUSTOMER"
		if prepared {
			role = account.Role
		}
		f.accounts[account.Email] = demoStoredAccount{
			identity: user{ID: uuid.NewString(), Email: account.Email, DisplayName: account.DisplayName, Role: role, Status: "ACTIVE"},
			password: account.Password,
		}
	}
}

func (f *demoAuthFake) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "POST /api/v1/auth/signup":
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.signupBodies = append(f.signupBodies, body)
		if len(body) != 3 || body["email"] == "" || body["password"] == "" || body["display_name"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, exists := f.accounts[body["email"]]; exists {
			w.WriteHeader(http.StatusConflict)
			return
		}
		u := user{ID: uuid.NewString(), Email: body["email"], DisplayName: body["display_name"], Role: "CUSTOMER", Status: "ACTIVE"}
		f.accounts[u.Email] = demoStoredAccount{identity: u, password: body["password"]}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(u)
	case "POST /api/v1/auth/login":
		f.loginCalls++
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		account, exists := f.accounts[body["email"]]
		if !exists || account.password != body["password"] {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		u := account.identity
		if f.loginOverride != nil {
			u = f.loginOverride(u)
		}
		token := fmt.Sprintf("opaque-private-test-token-%d", f.loginCalls)
		f.tokens[token] = u
		f.issuedTokens = append(f.issuedTokens, token)
		_ = json.NewEncoder(w).Encode(session{AccessToken: token, TokenType: "Bearer", ExpiresIn: 900, User: u})
	case "GET /api/v1/auth/me":
		f.meCalls++
		u, exists := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !exists {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.meOverride != nil {
			u = f.meOverride(u)
		}
		_ = json.NewEncoder(w).Encode(u)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

type demoRoleStoreFake struct {
	auth         *demoAuthFake
	findCalls    int
	promotions   []string
	findOverride func(user) (user, error)
}

func (s *demoRoleStoreFake) find(_ context.Context, id string) (user, error) {
	s.findCalls++
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	for _, account := range s.auth.accounts {
		if account.identity.ID == id {
			if s.findOverride != nil {
				return s.findOverride(account.identity)
			}
			return account.identity, nil
		}
	}
	return user{}, errors.New("fixture not found")
}

func (s *demoRoleStoreFake) promote(_ context.Context, before user, role string) error {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	account, exists := s.auth.accounts[before.Email]
	if !exists || account.identity != before || before.Role != "CUSTOMER" || before.Status != "ACTIVE" {
		return errors.New("role update did not match the confirmed account")
	}
	account.identity.Role = role
	s.auth.accounts[before.Email] = account
	s.promotions = append(s.promotions, role)
	return nil
}

func demoTestState(t *testing.T) *credentials {
	t.Helper()
	state, err := newState()
	if err != nil {
		t.Fatal("could not create test fixture state")
	}
	return state
}

func assertDemoOutputPrivate(t *testing.T, state *credentials, tokens []string, outputs ...[]byte) {
	t.Helper()
	for _, output := range outputs {
		if bytes.Contains(output, []byte("access_token")) || bytes.Contains(output, []byte("password")) {
			t.Error("public evidence contains a credential field")
		}
		for _, account := range fixtures(state) {
			if bytes.Contains(output, []byte(account.Password)) {
				t.Error("public evidence contains a fixture password")
			}
		}
		for _, token := range tokens {
			if bytes.Contains(output, []byte(token)) {
				t.Error("public evidence contains an access token")
			}
		}
	}
}

func TestPrepareUsesPublicSignupAndRepeatsStableFixtures(t *testing.T) {
	state := demoTestState(t)
	fake, api := newDemoAuthFake(t)
	store := &demoRoleStoreFake{auth: fake}
	persisted := 0
	persist := func() error { persisted++; return nil }
	var console bytes.Buffer
	first, err := prepare(context.Background(), state, api, store, persist, &console, false)
	if err != nil {
		t.Fatal("first fixture preparation failed")
	}
	ids := []string{state.Customer.ID, state.Staff.ID, state.Manager.ID}
	second, err := prepare(context.Background(), state, api, store, persist, &console, false)
	if err != nil {
		t.Fatal("existing fixtures did not replay successfully")
	}
	if !reflect.DeepEqual(ids, []string{state.Customer.ID, state.Staff.ID, state.Manager.ID}) || !reflect.DeepEqual(first.Accounts, second.Accounts) {
		t.Error("repeated preparation changed fixture identities")
	}
	if len(fake.accounts) != 3 || len(fake.signupBodies) != 6 || !reflect.DeepEqual(store.promotions, []string{"STAFF", "MANAGER"}) {
		t.Error("repeated preparation created or promoted extra accounts")
	}
	if persisted == 0 || !second.RealAuthLogin || !second.RealAuthMe || second.LocallyMintedJWT {
		t.Error("verification did not record real client authentication and private persistence")
	}
	for _, body := range fake.signupBodies {
		if _, present := body["role"]; present || len(body) != 3 {
			t.Error("public signup received a privileged role or extra field")
		}
	}
	for i, account := range fixtures(state) {
		if second.Accounts[i].Role != roles[i] || account.AccessToken == "" || account.Status != "ACTIVE" {
			t.Error("a fixture was not verified under its requested role")
		}
	}
	evidence, err := json.Marshal(second)
	if err != nil {
		t.Fatal("could not encode verification evidence")
	}
	assertDemoOutputPrivate(t, state, fake.issuedTokens, console.Bytes(), evidence)
}

func TestPrepareRejectsAuthDatabaseMismatchBeforePromotion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(user) user
	}{
		{"id", func(u user) user { u.ID = uuid.NewString(); return u }},
		{"email", func(u user) user { u.Email = "unrelated@example.test"; return u }},
		{"display_name", func(u user) user { u.DisplayName = "Unrelated account"; return u }},
		{"role", func(u user) user { u.Role = "MANAGER"; return u }},
		{"status", func(u user) user { u.Status = "DISABLED"; return u }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := demoTestState(t)
			fake, api := newDemoAuthFake(t)
			fake.seed(state, false)
			store := &demoRoleStoreFake{auth: fake, findOverride: func(u user) (user, error) {
				if u.Email == state.Staff.Email {
					u = tc.change(u)
				}
				return u, nil
			}}
			var console bytes.Buffer
			result, err := prepare(context.Background(), state, api, store, func() error { return nil }, &console, false)
			if err == nil || result.Status == "PASS" || len(store.promotions) != 0 || state.Staff.ID != "" {
				t.Error("mismatched Auth and database identities reached a privileged role update")
			}
			assertDemoOutputPrivate(t, state, fake.issuedTokens, console.Bytes())
		})
	}
}

func TestPrepareRejectsMeMismatchBeforeDatabaseMutation(t *testing.T) {
	state := demoTestState(t)
	fake, api := newDemoAuthFake(t)
	fake.seed(state, false)
	fake.meOverride = func(u user) user {
		if u.Email == state.Staff.Email {
			u.ID = uuid.NewString()
		}
		return u
	}
	store := &demoRoleStoreFake{auth: fake}
	_, err := prepare(context.Background(), state, api, store, func() error { return nil }, io.Discard, false)
	if err == nil || store.findCalls != 1 || len(store.promotions) != 0 || state.Staff.ID != "" {
		t.Error("mismatched /me response reached the privileged fixture database step")
	}
}

func TestPreparePinsCreatedSignupIdentityBeforeLogin(t *testing.T) {
	state := demoTestState(t)
	fake, api := newDemoAuthFake(t)
	fake.loginOverride = func(u user) user { u.ID = uuid.NewString(); return u }
	store := &demoRoleStoreFake{auth: fake}
	persistedID := ""
	_, err := prepare(context.Background(), state, api, store, func() error {
		persistedID = state.Customer.ID
		return nil
	}, io.Discard, false)
	created := fake.accounts[state.Customer.Email].identity.ID
	if err == nil || persistedID != created || state.Customer.ID != created || store.findCalls != 0 || len(store.promotions) != 0 {
		t.Error("signup and login identity mismatch was not rejected after pinning the created account")
	}
}

func TestVerifyOnlyNeverSignsUpOrPromotes(t *testing.T) {
	for _, prepared := range []bool{true, false} {
		name := "prepared"
		if !prepared {
			name = "unprepared"
		}
		t.Run(name, func(t *testing.T) {
			state := demoTestState(t)
			fake, api := newDemoAuthFake(t)
			fake.seed(state, prepared)
			store := &demoRoleStoreFake{auth: fake}
			result, err := prepare(context.Background(), state, api, store, func() error { return nil }, io.Discard, true)
			if len(fake.signupBodies) != 0 || len(store.promotions) != 0 {
				t.Error("verify-only performed signup or a role mutation")
			}
			if prepared && (err != nil || !result.VerifyOnly || len(result.Accounts) != 3 || fake.loginCalls != 3 || fake.meCalls != 3) {
				t.Error("prepared fixtures were not verified through login and /me")
			}
			if !prepared && (err == nil || result.Status == "PASS") {
				t.Error("verify-only accepted unprepared privileged fixtures")
			}
		})
	}
}

func TestPrepareStopsBeforePromotionWhenPersistenceFails(t *testing.T) {
	state := demoTestState(t)
	fake, api := newDemoAuthFake(t)
	fake.seed(state, false)
	store := &demoRoleStoreFake{auth: fake}
	persist := func() error {
		if state.Staff.ID != "" {
			return errors.New("private state write failed")
		}
		return nil
	}
	_, err := prepare(context.Background(), state, api, store, persist, io.Discard, false)
	if err == nil || len(store.promotions) != 0 || fake.accounts[state.Staff.Email].identity.Role != "CUSTOMER" {
		t.Error("failed private persistence allowed a privileged role grant")
	}
}

func TestPrepareRequiresFreshLoginToConfirmPromotedRole(t *testing.T) {
	state := demoTestState(t)
	fake, api := newDemoAuthFake(t)
	fake.seed(state, false)
	fake.loginOverride = func(u user) user {
		if u.Email == state.Staff.Email {
			u.Role = "CUSTOMER"
		}
		return u
	}
	store := &demoRoleStoreFake{auth: fake}
	result, err := prepare(context.Background(), state, api, store, func() error { return nil }, io.Discard, false)
	if err == nil || result.Status == "PASS" || !reflect.DeepEqual(store.promotions, []string{"STAFF"}) || state.Staff.AccessToken != "" {
		t.Error("stale Customer login was accepted as evidence of the promoted Staff role")
	}
}

func TestAuthClientDoesNotExposeReflectedFailureBodies(t *testing.T) {
	state := demoTestState(t)
	marker := state.Customer.Password
	for _, status := range []int{http.StatusInternalServerError, http.StatusOK} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, marker)
			}))
			defer server.Close()
			api := &authClient{origin: server.URL, client: server.Client()}
			_, err := api.authenticate(context.Background(), state.Customer)
			if err == nil || strings.Contains(err.Error(), marker) {
				t.Error("reflected secret response escaped the sanitized Auth error")
			}
		})
	}
}

func TestDemoConfigRestrictsAuthOriginAndDatabase(t *testing.T) {
	valid := config{authURL: "http://127.0.0.1:17011", databaseURL: "postgres://fixture:local_test_only@127.0.0.1:5432/auth_demo?sslmode=disable"}
	for _, tc := range []struct {
		name     string
		authURL  string
		dbURL    string
		accepted bool
	}{
		{name: "dedicated database", accepted: true},
		{name: "localhost", authURL: "http://localhost:17011", accepted: true},
		{name: "ipv6 loopback", authURL: "http://[::1]:17011", accepted: true},
		{name: "remote hostname", authURL: "https://auth.example.test"},
		{name: "remote address", authURL: "http://192.0.2.1:17011"},
		{name: "origin userinfo", authURL: "http://fixture:local_test_only@127.0.0.1:17011"},
		{name: "origin path", authURL: "http://127.0.0.1:17011/api/v1/auth"},
		{name: "origin query", authURL: "http://127.0.0.1:17011?destination=other"},
		{name: "origin fragment", authURL: "http://127.0.0.1:17011#other"},
		{name: "other database", dbURL: "postgres://127.0.0.1/auth_other"},
		{name: "database query override", dbURL: "postgres://127.0.0.1/auth_demo?database=auth_other"},
		{name: "dbname query override", dbURL: "postgres://127.0.0.1/auth_demo?dbname=auth_other"},
		{name: "encoded other database", dbURL: "postgres://127.0.0.1/auth_demo%2Fother"},
		{name: "database fragment", dbURL: "postgres://127.0.0.1/auth_demo#other"},
		{name: "database missing host", dbURL: "postgres:///auth_demo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			if tc.authURL != "" {
				cfg.authURL = tc.authURL
			}
			if tc.dbURL != "" {
				cfg.databaseURL = tc.dbURL
			}
			if err := validateConfig(cfg); (err == nil) != tc.accepted {
				t.Error("configuration origin or dedicated database guard returned the wrong outcome")
			}
		})
	}
}

func TestInvalidFixtureStateStopsBeforeNetworkAndDatabaseWork(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*credentials)
	}{
		{"schema", func(s *credentials) { s.SchemaVersion++ }},
		{"namespace", func(s *credentials) { s.Namespace = "wrong" }},
		{"missing fixture", func(s *credentials) { s.Staff = nil }},
		{"role", func(s *credentials) { s.Staff.Role = "MANAGER" }},
		{"email", func(s *credentials) { s.Staff.Email = "other@example.test" }},
		{"display name", func(s *credentials) { s.Staff.DisplayName = "Other" }},
		{"short credential", func(s *credentials) { s.Staff.Password = "short" }},
		{"nonhex credential", func(s *credentials) { s.Staff.Password = strings.Repeat("z", 64) }},
		{"nil account id", func(s *credentials) { s.Staff.ID = uuid.Nil.String() }},
		{"repeated account id", func(s *credentials) {
			s.Customer.ID = "abcdefab-abcd-4abc-8abc-abcdefabcdef"
			s.Staff.ID = strings.ToUpper(s.Customer.ID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := demoTestState(t)
			tc.change(state)
			fake, api := newDemoAuthFake(t)
			store := &demoRoleStoreFake{auth: fake}
			persists := 0
			_, err := prepare(context.Background(), state, api, store, func() error { persists++; return nil }, io.Discard, false)
			if err == nil || len(fake.signupBodies) != 0 || fake.loginCalls != 0 || store.findCalls != 0 || len(store.promotions) != 0 || persists != 0 {
				t.Error("invalid fixture state reached provisioning work")
			}
		})
	}
	if err := validateState(nil); err == nil {
		t.Error("nil private state was accepted")
	}
}

func TestPrivateStateRoundTripRepairsPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions are not supported on Windows")
	}
	dir := filepath.Join(t.TempDir(), "state")
	state := demoTestState(t)
	if err := writePrivate(dir, "credentials.json", state); err != nil {
		t.Fatal("could not write private fixture state")
	}
	path := filepath.Join(dir, "credentials.json")
	if os.Chmod(dir, 0o755) != nil || os.Chmod(path, 0o644) != nil {
		t.Fatal("could not relax test state permissions")
	}
	loaded, err := loadState(dir, true)
	if err != nil || !reflect.DeepEqual(state, loaded) {
		t.Fatal("private fixture state did not round trip")
	}
	if err := writePrivate(dir, "verification.json", verification{Accounts: []user{{Role: "CUSTOMER"}}}); err != nil {
		t.Fatal("could not write private verification")
	}
	for _, target := range []struct {
		path string
		mode os.FileMode
	}{{dir, 0o700}, {path, 0o600}, {filepath.Join(dir, "verification.json"), 0o600}} {
		info, err := os.Stat(target.path)
		if err != nil || info.Mode().Perm() != target.mode {
			t.Error("private demo directory or file permissions are too broad")
		}
	}
	if err := writePrivate(dir, "credentials.json", loaded); err != nil {
		t.Fatal("could not replace private fixture state")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Error("private state replacement left temporary files behind")
	}
}

func TestLoadStateRejectsUnsafeOrAmbiguousFiles(t *testing.T) {
	for _, tc := range []string{"missing", "symlink", "directory", "oversized", "unknown field", "trailing JSON"} {
		t.Run(tc, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "credentials.json")
			state := demoTestState(t)
			body, err := json.Marshal(state)
			if err != nil {
				t.Fatal("could not encode test state")
			}
			switch tc {
			case "missing":
			case "symlink":
				if runtime.GOOS == "windows" {
					t.Skip("symlinks require elevated privileges on Windows")
				}
				target := filepath.Join(t.TempDir(), "target.json")
				if os.WriteFile(target, body, 0o600) != nil || os.Symlink(target, path) != nil {
					t.Fatal("could not create symlink fixture")
				}
			case "directory":
				if os.Mkdir(path, 0o700) != nil {
					t.Fatal("could not create directory fixture")
				}
			default:
				switch tc {
				case "oversized":
					body = bytes.Repeat([]byte("x"), 64<<10+1)
				case "unknown field":
					var object map[string]any
					if json.Unmarshal(body, &object) != nil {
						t.Fatal("could not prepare ambiguous test state")
					}
					object["grant_role"] = "MANAGER"
					body, err = json.Marshal(object)
					if err != nil {
						t.Fatal("could not encode ambiguous test state")
					}
				case "trailing JSON":
					body = append(body, []byte("{}")...)
				}
				if os.WriteFile(path, body, 0o600) != nil {
					t.Fatal("could not write invalid test state")
				}
			}
			if _, err := loadState(dir, true); err == nil {
				t.Error("unsafe or ambiguous private fixture file was accepted")
			}
		})
	}
}

func TestPrivateDirectoryRejectsSymlinkAndAtomicWriteProtectsTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on Windows")
	}
	root := t.TempDir()
	targetDir := filepath.Join(root, "target")
	if os.Mkdir(targetDir, 0o700) != nil || os.Symlink(targetDir, filepath.Join(root, "linked")) != nil {
		t.Fatal("could not create linked state directory")
	}
	if err := writePrivate(filepath.Join(root, "linked"), "credentials.json", demoTestState(t)); err == nil {
		t.Error("linked private directory was accepted")
	}
	if _, err := os.Stat(filepath.Join(targetDir, "credentials.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("rejected linked directory was mutated")
	}
	target := filepath.Join(root, "untouched.json")
	original := []byte("unrelated local file")
	if os.WriteFile(target, original, 0o600) != nil || os.Symlink(target, filepath.Join(targetDir, "credentials.json")) != nil {
		t.Fatal("could not create linked credential file")
	}
	if err := writePrivate(targetDir, "credentials.json", demoTestState(t)); err != nil {
		t.Fatal("atomic private file replacement failed")
	}
	unchanged, err := os.ReadFile(target)
	info, statErr := os.Lstat(filepath.Join(targetDir, "credentials.json"))
	if err != nil || !bytes.Equal(unchanged, original) || statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Error("atomic fixture write followed a file symlink or weakened permissions")
	}
}
