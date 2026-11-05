package metrics

import "strings"

type ServiceState struct {
	Unit   string `json:"unit"`
	Load   string `json:"load"`
	Active string `json:"active"`
	Sub    string `json:"sub"`
}
type SystemdSnapshot struct {
	CollectedAt int64          `json:"collected_at"`
	Available   bool           `json:"available"`
	Truncated   bool           `json:"truncated"`
	Units       []ServiceState `json:"units"`
}

func (s SystemdSnapshot) Validate() error {
	if s.CollectedAt <= 0 || len(s.Units) > 256 || (!s.Available && len(s.Units) > 0) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, u := range s.Units {
		if !strings.HasSuffix(u.Unit, ".service") || seen[u.Unit] {
			return ErrInvalid
		}
		seen[u.Unit] = true
		for _, v := range []string{u.Unit, u.Load, u.Active, u.Sub} {
			if len(v) == 0 || len(v) > 256 || strings.ContainsAny(v, "\x00\n\r") {
				return ErrInvalid
			}
		}
	}
	return nil
}
