package rbac

type Role string
type Action string

const (
	Owner          Role   = "Owner"
	Admin          Role   = "Admin"
	Operator       Role   = "Operator"
	Viewer         Role   = "Viewer"
	HostRead       Action = "host.read"
	HostManage     Action = "host.manage"
	Collect        Action = "host.collect"
	AuditRead      Action = "audit.read"
	IdentityManage Action = "identity.manage"
	Backup         Action = "backup.manage"
	OwnerManage    Action = "owner.manage"
)

type Grant struct {
	Role   Role   `json:"role"`
	All    bool   `json:"all_hosts"`
	HostID string `json:"host_id,omitempty"`
}
type Principal struct {
	ID      string
	Enabled bool
	Grants  []Grant
}

func (p Principal) Allows(action Action, hostID string) bool {
	if !p.Enabled {
		return false
	}
	switch action {
	case HostRead, HostManage, Collect, AuditRead, IdentityManage, Backup, OwnerManage:
	default:
		return false
	}
	for _, g := range p.Grants {
		if g.Role == Owner && g.All {
			return true
		}
		if action == Backup || action == OwnerManage {
			continue
		}
		if !g.All && (hostID == "" || hostID != g.HostID) {
			continue
		}
		switch action {
		case HostRead:
			if g.Role == Admin || g.Role == Operator || g.Role == Viewer {
				return true
			}
		case HostManage, AuditRead:
			if g.Role == Admin {
				return true
			}
		case Collect:
			if g.Role == Admin || g.Role == Operator {
				return true
			}
		case IdentityManage:
			if g.Role == Admin && g.All && hostID == "" {
				return true
			}
		}
	}
	return false
}
