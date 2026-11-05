import type { Grant } from "@/lib/api";

// Mirrors internal/rbac for display only. The server rechecks every request.

export function isOwner(grants: Grant[]) {
  return grants.some((g) => g.role === "Owner" && g.all_hosts);
}

function globalAdmin(grants: Grant[]) {
  return isOwner(grants) || grants.some((g) => g.role === "Admin" && g.all_hosts);
}

export const canManageIdentity = globalAdmin;
export const canCreateHosts = globalAdmin;
export const canReadAuditSettings = globalAdmin;

export function canReadAudit(grants: Grant[]) {
  return isOwner(grants) || grants.some((g) => g.role === "Admin");
}

export function canReadHosts(grants: Grant[]) {
  return grants.length > 0;
}

export function canManageHost(grants: Grant[], hostId: string) {
  return (
    isOwner(grants) ||
    grants.some((g) => g.role === "Admin" && (g.all_hosts || g.host_id === hostId))
  );
}
