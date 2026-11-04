package controlplane

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sudatti624/AmuOS/internal/security"
)

func TestAuthorizationContractCoversEveryRoute(t *testing.T) {
	db, h, owner := accountFixture(t)
	file, err := os.Open("../../docs/API_AUTHORIZATION.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string][]string{}
	public := map[string]string{"GET /healthz": "public", "POST /api/v1/setup": "bootstrap", "POST /api/v1/auth/login": "password", "POST /api/v1/invitations/accept": "invitation"}
	for _, row := range rows[1:] {
		if len(row) != 7 {
			t.Fatal("invalid contract row", row)
		}
		key := row[0] + " " + row[1]
		if row[2] != "session" && public[key] != row[2] {
			t.Errorf("unapproved public route: %s", key)
		}
		if row[5] != "yes" && row[5] != "no" {
			t.Errorf("invalid CSRF contract: %s", key)
		}
		if _, exists := documented[key]; exists {
			t.Fatal("duplicate route", key)
		}
		documented[key] = row
	}
	actual := map[string]bool{}
	err = chi.Walk(h.(chi.Routes), func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + path
		actual[key] = true
		if _, ok := documented[key]; !ok {
			t.Errorf("route has no authorization contract: %s", key)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key, row := range documented {
		if !actual[key] {
			t.Errorf("contract has no implementation: %s", key)
			continue
		}
		if row[2] != "session" {
			continue
		}
		body := map[string]any{"name": "fixture"}
		path := strings.ReplaceAll(row[1], "{id}", owner.UserID)
		switch {
		case strings.HasSuffix(path, "/enabled"), strings.HasSuffix(path, "/owner"):
			body = map[string]any{"enabled": true}
		case strings.HasSuffix(path, "/members"):
			body = map[string]any{"user_ids": []string{}}
		case strings.HasSuffix(path, "/grants"):
			body = map[string]any{"grants": []any{}}
		case path == "/api/v1/ownership/transfer":
			body = map[string]any{"user_id": "other-user"}
		case path == "/api/v1/audit/retention":
			body = map[string]any{"retention_days": 180}
		case path == "/api/v1/audit/retention/run":
			body = map[string]any{}
		case path == "/api/v1/auth/reauthenticate":
			body = map[string]any{"password": "correct horse battery staple"}
		case path == "/api/v1/auth/password":
			body = map[string]any{"current_password": "correct horse battery staple", "new_password": "another valid password"}
		case path == "/api/v1/invitations":
			body = map[string]any{"username": "member", "grants": []any{map[string]any{"role": "Viewer", "all_hosts": true}}}
		}
		encoded, _ := json.Marshal(body)
		request := func(authenticated bool) *httptest.ResponseRecorder {
			r := httptest.NewRequest(row[0], "https://amuos.test"+path, bytes.NewReader(encoded))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://amuos.test")
			r.Header.Set("Idempotency-Key", security.Token())
			if authenticated {
				r.AddCookie(&http.Cookie{Name: cookieName, Value: owner.Token})
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			return w
		}
		if w := request(false); w.Code != 401 {
			t.Errorf("%s anonymous got %d: %s", key, w.Code, w.Body.String())
		}
		if row[5] == "yes" {
			if w := request(true); w.Code != 403 {
				t.Errorf("%s missing CSRF got %d: %s", key, w.Code, w.Body.String())
			}
		}
	}
	// All negative checks must leave business state unchanged.
	hosts, err := db.Hosts(t.Context(), owner.Token, time.Now())
	if err != nil || len(hosts) != 0 {
		t.Fatal("unauthorized mutation", hosts, err)
	}
	groups, err := db.Groups(t.Context(), owner.Token, time.Now())
	if err != nil || len(groups) != 0 {
		t.Fatal("unauthorized group mutation", groups, err)
	}
	users, err := db.Users(t.Context(), owner.Token, time.Now())
	if err != nil || len(users) != 1 || !users[0].Enabled {
		t.Fatal("unauthorized user mutation", users, err)
	}
}
