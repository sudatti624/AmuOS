package controlplane

import (
	"encoding/json"
	"testing"

	"github.com/sudatti624/AmuOS/internal/store"
)

func TestAuditRetentionHTTP(t *testing.T) {
	_, h, owner := accountFixture(t)
	w := accountHTTP(t, h, "GET", "/api/v1/audit/retention", nil, owner, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var settings store.AuditRetention
	if err := json.Unmarshal(w.Body.Bytes(), &settings); err != nil || settings.Days != 180 {
		t.Fatal(settings, err)
	}
	if w := accountHTTP(t, h, "PUT", "/api/v1/audit/retention", map[string]int{"retention_days": 30}, owner, owner.CSRF); w.Code != 403 {
		t.Fatalf("missing reauth: %d", w.Code)
	}
	if w := accountHTTP(t, h, "POST", "/api/v1/auth/reauthenticate", map[string]string{"password": "correct horse battery staple"}, owner, owner.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "PUT", "/api/v1/audit/retention", map[string]any{}, owner, owner.CSRF); w.Code != 400 {
		t.Fatalf("omitted days accepted: %d", w.Code)
	}
	if w := accountHTTP(t, h, "PUT", "/api/v1/audit/retention", map[string]int{"retention_days": 0}, owner, owner.CSRF); w.Code != 400 {
		t.Fatalf("invalid days accepted: %d", w.Code)
	}
	if w := accountHTTP(t, h, "PUT", "/api/v1/audit/retention", map[string]int{"retention_days": 30}, owner, owner.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "POST", "/api/v1/audit/retention/run", map[string]any{"ids": []int{1}}, owner, owner.CSRF); w.Code != 400 {
		t.Fatalf("individual deletion input accepted: %d", w.Code)
	}
	if w := accountHTTP(t, h, "POST", "/api/v1/audit/retention/run", nil, owner, owner.CSRF); w.Code != 400 {
		t.Fatalf("null purge request accepted: %d", w.Code)
	}
	if w := accountHTTP(t, h, "POST", "/api/v1/audit/retention/run", map[string]any{}, owner, ""); w.Code != 403 {
		t.Fatalf("missing CSRF: %d", w.Code)
	}
	w = accountHTTP(t, h, "POST", "/api/v1/audit/retention/run", map[string]any{}, owner, owner.CSRF)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result store.AuditPurge
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Days != 30 || result.Deleted != 0 {
		t.Fatal(result, err)
	}
	if w := accountHTTP(t, h, "DELETE", "/api/v1/audit/1", nil, owner, owner.CSRF); w.Code != 404 {
		t.Fatalf("individual audit delete exists: %d", w.Code)
	}
}
