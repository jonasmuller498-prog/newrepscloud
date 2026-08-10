package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIRoleAuthorization(t *testing.T) {
	protector, err := NewProtector([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	api := &API{
		config: Config{OperatorToken: "operator-secret", ApproverToken: "approver-secret"},
		store:  &Store{protector: protector},
	}
	target := api.require("approver")(func(w http.ResponseWriter, r *http.Request) {
		if got := principal(r).Role; got != "approver" {
			t.Fatalf("role = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	tests := []struct {
		name, token string
		status      int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"invalid", "wrong", http.StatusUnauthorized},
		{"operator forbidden", "operator-secret", http.StatusForbidden},
		{"approver", "approver-secret", http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/approve", nil)
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			target(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d", response.Code, test.status)
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, name := range []string{
		"Content-Security-Policy", "Referrer-Policy", "X-Content-Type-Options",
		"X-Frame-Options", "Permissions-Policy",
	} {
		if response.Header().Get(name) == "" {
			t.Errorf("missing %s", name)
		}
	}
}

func TestTokenComparison(t *testing.T) {
	if !secureTokenEqual("same", "same") || secureTokenEqual("same", "different") {
		t.Fatal("constant-time token comparison returned the wrong result")
	}
}
