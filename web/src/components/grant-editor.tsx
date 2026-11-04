import { PlusIcon, Trash2Icon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/input";
import type { Grant, Host, Role } from "@/lib/api";

const roles: Role[] = ["Admin", "Operator", "Viewer"];
export const maxGrants = 32;

// Edits non-Owner fixed-role grants. Owner is managed by its own operation.
export function GrantEditor({
  idPrefix,
  value,
  onChange,
  hosts,
}: {
  idPrefix: string;
  value: Grant[];
  onChange: (grants: Grant[]) => void;
  hosts: Host[];
}) {
  const update = (index: number, grant: Grant) =>
    onChange(value.map((g, i) => (i === index ? grant : g)));
  return (
    <fieldset className="grid gap-2">
      <legend className="mb-1 text-sm font-medium">ロールと範囲</legend>
      {value.length === 0 && <p className="text-sm text-muted-foreground">割り当てなし</p>}
      {value.map((grant, index) => (
        <div key={index} className="flex flex-wrap items-center gap-2">
          <NativeSelect
            aria-label={`ロール ${index + 1}`}
            id={`${idPrefix}-role-${index}`}
            className="w-36"
            value={grant.role}
            onChange={(event) => update(index, { ...grant, role: event.target.value as Role })}
          >
            {roles.map((role) => (
              <option key={role} value={role}>
                {role}
              </option>
            ))}
          </NativeSelect>
          <NativeSelect
            aria-label={`範囲 ${index + 1}`}
            className="min-w-40 flex-1"
            value={grant.all_hosts ? "" : (grant.host_id ?? "")}
            onChange={(event) =>
              update(
                index,
                event.target.value === ""
                  ? { role: grant.role, all_hosts: true }
                  : { role: grant.role, all_hosts: false, host_id: event.target.value },
              )
            }
          >
            <option value="">全ホスト</option>
            {hosts.map((host) => (
              <option key={host.id} value={host.id}>
                ホスト: {host.name}
              </option>
            ))}
          </NativeSelect>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            onClick={() => onChange(value.filter((_, i) => i !== index))}
            aria-label={`ロール ${index + 1} を削除`}
          >
            <Trash2Icon />
          </Button>
        </div>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="w-fit"
        disabled={value.length >= maxGrants}
        onClick={() => onChange([...value, { role: "Viewer", all_hosts: true }])}
      >
        <PlusIcon />
        ロールを追加
      </Button>
    </fieldset>
  );
}

export function duplicateGrant(grants: Grant[]) {
  const keys = grants.map((g) => `${g.role}:${g.all_hosts ? "*" : g.host_id}`);
  return new Set(keys).size !== keys.length;
}

export function withoutOwner(grants: Grant[]) {
  return grants.filter((g) => g.role !== "Owner");
}
