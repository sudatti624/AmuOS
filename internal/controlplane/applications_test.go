package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/store"
)

func TestApplicationHTTPContract(t *testing.T) {
	db, h, owner := accountFixture(t)
	host, err := db.CreateHost(t.Context(), owner.Token, owner.CSRF, "apps-host", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/hosts/" + host.ID + "/applications"
	path := base + "/example"
	input := map[string]any{"expected_revision": 0, "config": map[string]any{"runtime": "python", "read_roles": []string{"Admin"}, "operate_roles": []string{}}}
	for _, tc := range []struct {
		method  string
		session store.Session
		csrf    string
		want    int
	}{
		{"PUT", store.Session{}, "", 401},
		{"PUT", owner, "", 403},
		{"PUT", owner, owner.CSRF, 200},
		{"PUT", owner, owner.CSRF, 200},
		{"GET", owner, "", 200},
	} {
		w := accountHTTP(t, h, tc.method, path, input, tc.session, tc.csrf)
		if w.Code != tc.want {
			t.Fatalf("%s got %d: %s", tc.method, w.Code, w.Body.String())
		}
		if tc.want == 200 {
			var a store.Application
			if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil || a.Revision != 1 {
				t.Fatal(a, err)
			}
		}
	}
	// Unknown fields must not become a command or secret storage channel.
	input["command"] = "never-execute-this"
	if w := accountHTTP(t, h, "PUT", path, input, owner, owner.CSRF); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	delete(input, "command")
	input["expected_revision"] = 4
	if w := accountHTTP(t, h, "PUT", path, input, owner, owner.CSRF); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "GET", base, nil, owner, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "example") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "DELETE", path, map[string]int{"expected_revision": 1}, owner, owner.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "GET", path, nil, owner, ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	// There is no execution route in this implementation.
	if w := accountHTTP(t, h, "POST", path+"/start", map[string]any{}, owner, owner.CSRF); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
}
