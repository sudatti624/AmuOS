package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/security"
	"github.com/sudatti624/AmuOS/internal/store"
)

func TestTaskAPI(t *testing.T) {
	db, h, owner := accountFixture(t)
	host, err := db.CreateHost(context.Background(), owner.Token, owner.CSRF, "task-host", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://amuos.test"+path, strings.NewReader(body))
		r.Header.Set("Origin", "https://amuos.test")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", owner.CSRF)
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		r.AddCookie(&http.Cookie{Name: cookieName, Value: owner.Token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	create := "/api/v1/hosts/" + host.ID + "/tasks"
	if w := call("POST", create, "", `{"operation":"collect"}`); w.Code != 400 {
		t.Fatalf("missing key: %d", w.Code)
	}
	key := security.Token()
	// Only read-only collection is offered; there is no command input.
	for _, body := range []string{`{"operation":"exec"}`, `{"operation":"collect","command":"id"}`} {
		if w := call("POST", create, key, body); w.Code != 400 {
			t.Fatalf("%s accepted: %d", body, w.Code)
		}
	}
	if w := call("POST", "/api/v1/hosts/missing/tasks", key, `{"operation":"collect"}`); w.Code != 404 {
		t.Fatalf("missing host: %d", w.Code)
	}
	first, second := call("POST", create, key, `{"operation":"collect"}`), call("POST", create, key, `{"operation":"collect"}`)
	if first.Code != 201 || second.Code != 201 || second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal(first.Code, second.Code, second.Body.String())
	}
	var task, replay store.Task
	if err := json.Unmarshal(first.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if task.ID != replay.ID || task.State != "queued" || task.HostID != host.ID {
		t.Fatalf("task %+v replay %+v", task, replay)
	}

	w := call("GET", "/api/v1/tasks", "", "")
	var list struct{ Items []store.Task }
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 || len(list.Items) != 1 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	if w := call("GET", "/api/v1/tasks/"+task.ID, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"queued"`) {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	if w := call("GET", "/api/v1/tasks/missing", "", ""); w.Code != 404 {
		t.Fatalf("missing task: %d", w.Code)
	}
	if w := call("POST", "/api/v1/tasks/"+task.ID+"/resolve", "", `{"resolution":"succeeded","evidence":"checked"}`); w.Code != 409 {
		t.Fatalf("resolved a queued task: %d", w.Code)
	}
	if w := call("POST", "/api/v1/tasks/"+task.ID+"/cancel", "", `null`); w.Code != 400 {
		t.Fatalf("null cancel body: %d", w.Code)
	}
	if w := call("POST", "/api/v1/tasks/"+task.ID+"/cancel", "", `{}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"cancelled"`) {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	if w := call("POST", "/api/v1/tasks/"+task.ID+"/cancel", "", `{}`); w.Code != 409 {
		t.Fatalf("cancel twice: %d", w.Code)
	}
}
