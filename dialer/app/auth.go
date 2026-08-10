package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

type roleKey struct{}

type Principal struct {
	Role, Actor string
}

func (a *API) authenticate(r *http.Request) (Principal, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return Principal{}, false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	operator := secureTokenEqual(token, a.config.OperatorToken)
	approver := secureTokenEqual(token, a.config.ApproverToken)
	switch {
	case operator:
		return Principal{"operator", a.store.protector.ActorID(token)}, true
	case approver:
		return Principal{"approver", a.store.protector.ActorID(token)}, true
	default:
		return Principal{}, false
	}
}

func secureTokenEqual(provided, expected string) bool {
	providedHash := sha256.Sum256([]byte(provided))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) == 1
}

func (a *API) require(roles ...string) func(http.HandlerFunc) http.HandlerFunc {
	allowed := make(map[string]bool, len(roles))
	for _, role := range roles {
		allowed[role] = true
	}
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			principal, ok := a.authenticate(r)
			if !ok {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeProblem(w, http.StatusUnauthorized, "unauthorized", "A valid role token is required.")
				return
			}
			if !allowed[principal.Role] {
				writeProblem(w, http.StatusForbidden, "forbidden", "This action requires a different role.")
				return
			}
			ctx := context.WithValue(r.Context(), roleKey{}, principal)
			next(w, r.WithContext(ctx))
		}
	}
}

func principal(r *http.Request) Principal {
	value, _ := r.Context().Value(roleKey{}).(Principal)
	return value
}
