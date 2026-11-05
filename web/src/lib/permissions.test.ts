import { describe, expect, it } from "vite-plus/test";
import type { Grant } from "@/lib/api";
import {
  canCollect,
  canCreateHosts,
  canManageIdentity,
  canReadAudit,
  canReadHosts,
  isOwner,
} from "@/lib/permissions";

const owner: Grant[] = [{ role: "Owner", all_hosts: true }];
const globalAdmin: Grant[] = [{ role: "Admin", all_hosts: true }];
const hostAdmin: Grant[] = [{ role: "Admin", all_hosts: false, host_id: "h1" }];
const viewer: Grant[] = [{ role: "Viewer", all_hosts: true }];

describe("permissions mirror internal/rbac for display", () => {
  it("treats only a global Owner grant as owner", () => {
    expect(isOwner(owner)).toBe(true);
    expect(isOwner([{ role: "Owner", all_hosts: false, host_id: "h1" }])).toBe(false);
    expect(isOwner(globalAdmin)).toBe(false);
  });

  it("limits identity and host creation to Owner and global Admin", () => {
    for (const check of [canManageIdentity, canCreateHosts]) {
      expect(check(owner)).toBe(true);
      expect(check(globalAdmin)).toBe(true);
      expect(check(hostAdmin)).toBe(false);
      expect(check(viewer)).toBe(false);
      expect(check([])).toBe(false);
    }
  });

  it("allows scoped Admin to read audit but not Viewer", () => {
    expect(canReadAudit(hostAdmin)).toBe(true);
    expect(canReadAudit(viewer)).toBe(false);
  });

  it("requires at least one grant to read hosts", () => {
    expect(canReadHosts(viewer)).toBe(true);
    expect(canReadHosts([])).toBe(false);
  });
  it("allows collection to Owners and to Admins and Operators in scope", () => {
    const operator: Grant[] = [{ role: "Operator", all_hosts: false, host_id: "h1" }];
    expect(canCollect(owner, "h2")).toBe(true);
    expect(canCollect(hostAdmin, "h1")).toBe(true);
    expect(canCollect(operator, "h1")).toBe(true);
    expect(canCollect(operator, "h2")).toBe(false);
    expect(canCollect(viewer, "h1")).toBe(false);
  });
});
