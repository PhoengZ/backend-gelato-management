package tests

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCredentialJSONRejectsAmbiguity(t *testing.T) {
	for _, body := range []string{
		`{"email":"user@example.com","email":"other@example.com","password":"password123","display_name":"User"}`,
		`{"EMAIL":"user@example.com","password":"password123","display_name":"User"}`,
		`{"email":"user@example.com","password":"password123","display_name":null}`,
		`{"email":"user@example.com","password":"password123"}`,
		`null`,
	} {
		t.Run(body, func(t *testing.T) {
			stub := &stubAuthService{signUpResult: testUser()}
			app, _ := newHandlerApp(stub)
			r := httptest.NewRequest("POST", "/api/v1/auth/signup", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			response, err := app.Test(r)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != 400 || stub.signUpCalls != 0 {
				t.Fatalf("ambiguous credentials reached service: status=%d calls=%d", response.StatusCode, stub.signUpCalls)
			}
		})
	}
}

func TestMeRejectsDuplicateAuthorizationAndPreventsCaching(t *testing.T) {
	app, tokens := newHandlerApp(&stubAuthService{meResult: testUser()})
	token, err := tokens.Issue(testUser())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	r.Header.Add("Authorization", "Bearer "+token)
	r.Header.Add("Authorization", "Bearer "+token)
	response, err := app.Test(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatalf("duplicate token accepted: %d", response.StatusCode)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("identity response can be cached")
	}
}
