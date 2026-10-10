package transport

import (
	"batch-inventory-service/internal/auth"
	"batch-inventory-service/internal/inventory"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
)

func reply(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func failure(w http.ResponseWriter, err error) {
	httpCode, _, code, msg := classify(err)
	reply(w, httpCode, map[string]string{"code": code, "message": msg})
}
func authFailure(w http.ResponseWriter, code int) {
	reason := "UNAUTHORIZED"
	if code == 403 {
		reason = "FORBIDDEN"
	}
	reply(w, code, map[string]string{"code": reason, "message": http.StatusText(code)})
}
func identity(w http.ResponseWriter, r *http.Request, v *auth.Verifier, required bool) (*auth.Claims, bool) {
	headers := r.Header.Values("Authorization")
	if len(headers) == 0 && !required {
		return nil, true
	}
	if len(headers) != 1 {
		authFailure(w, 401)
		return nil, false
	}
	parts := strings.Fields(headers[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		authFailure(w, 401)
		return nil, false
	}
	claims, err := v.Parse(parts[1])
	if err != nil {
		authFailure(w, 401)
		return nil, false
	}
	return claims, true
}

// Reject duplicate keys before decoding into typed inputs, including nested JSON.
func uniqueJSON(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := k.(string)
			if !ok || seen[key] {
				return inventory.ErrInvalid
			}
			seen[key] = true
			if err = uniqueJSON(d); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	case '[':
		for d.More() {
			if err = uniqueJSON(d); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	default:
		return inventory.ErrInvalid
	}
}
func decode(w http.ResponseWriter, r *http.Request, out any) error {
	if ct := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); ct != "application/json" {
		return inventory.ErrInvalid
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		return inventory.ErrInvalid
	}
	trim := bytes.TrimSpace(raw)
	if len(trim) == 0 || trim[0] != '{' {
		return inventory.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if err = uniqueJSON(d); err != nil {
		return inventory.ErrInvalid
	}
	if _, err = d.Token(); err != io.EOF {
		return inventory.ErrInvalid
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return inventory.ErrInvalid
	}
	allowed := map[string]bool{}
	typ := reflect.TypeOf(out).Elem()
	for i := 0; i < typ.NumField(); i++ {
		allowed[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	for k, v := range fields {
		if !allowed[k] || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return inventory.ErrInvalid
		}
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return inventory.ErrInvalid
	}
	return nil
}
func NewHTTP(svc *inventory.Service, v *auth.Verifier, timeout time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		if err := svc.DB.Ping(r.Context()); err != nil {
			reply(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		reply(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /api/v1/inventory/availability", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := identity(w, r, v, false); !ok {
			return
		}
		if !queryValid(r, "flavor_id") {
			failure(w, inventory.ErrInvalid)
			return
		}
		items, t, err := svc.Availability(r.Context(), r.URL.Query().Get("flavor_id"))
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, 200, map[string]any{"items": items, "as_of": t})
	})
	protect := func(manager bool, fn func(http.ResponseWriter, *http.Request, *auth.Claims)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			claims, ok := identity(w, r, v, true)
			if !ok {
				return
			}
			if claims.Role != auth.RoleManager && (manager || claims.Role != auth.RoleStaff) {
				authFailure(w, 403)
				return
			}
			fn(w, r, claims)
		}
	}
	mux.HandleFunc("GET /api/v1/inventory/batches", protect(false, func(w http.ResponseWriter, r *http.Request, _ *auth.Claims) {
		if !queryValid(r, "flavor_id", "status") {
			failure(w, inventory.ErrInvalid)
			return
		}
		items, err := svc.List(r.Context(), r.URL.Query().Get("flavor_id"), r.URL.Query().Get("status"))
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, 200, map[string]any{"items": items})
	}))
	mux.HandleFunc("GET /api/v1/inventory/batches/{id}", protect(false, func(w http.ResponseWriter, r *http.Request, _ *auth.Claims) {
		b, err := svc.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, 200, b)
	}))
	mux.HandleFunc("POST /api/v1/inventory/batches", protect(true, func(w http.ResponseWriter, r *http.Request, _ *auth.Claims) {
		var in inventory.CreateInput
		if err := decode(w, r, &in); err != nil {
			failure(w, err)
			return
		}
		b, err := svc.Create(r.Context(), in)
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, 201, b)
	}))
	mux.HandleFunc("POST /api/v1/inventory/batches/{id}/waste", protect(false, func(w http.ResponseWriter, r *http.Request, c *auth.Claims) {
		var in inventory.WasteInput
		if err := decode(w, r, &in); err != nil {
			failure(w, err)
			return
		}
		result, err := svc.Waste(r.Context(), r.PathValue("id"), c.Subject, in)
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, 201, result)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}
func queryValid(r *http.Request, allowed ...string) bool {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return false
	}
	for k, v := range q {
		found := false
		for _, a := range allowed {
			if k == a {
				found = true
			}
		}
		if !found || len(v) != 1 || v[0] == "" {
			return false
		}
	}
	return true
}
