import { useCallback, useState, type FormEvent } from "react";
import { CopyIcon, PlusIcon } from "lucide-react";
import { useConfirm } from "@/app/confirm";
import { useNotify } from "@/app/notifications";
import { ReauthCancelled, useOwnerAction } from "@/app/reauth";
import { useMe, useSession } from "@/app/session";
import { useLoad } from "@/app/use-load";
import { ErrorText, Field, Loading, PageHeader, Status } from "@/components/common";
import { duplicateGrant, GrantEditor, withoutOwner } from "@/components/grant-editor";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableEmpty,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  api,
  errorMessage,
  usernamePattern,
  type Grant,
  type Group,
  type Host,
  type Invitation,
  type Items,
  type User,
} from "@/lib/api";
import { formatTime, grantLabel } from "@/lib/format";
import { isOwner } from "@/lib/permissions";

interface Directory {
  users: (User & { grants: Grant[] })[];
  groups: Group[];
  hosts: Host[];
}

async function loadDirectory(): Promise<Directory> {
  const [users, groups, hosts] = await Promise.all([
    api.get<Items<User>>("/users").then((r) => r.items),
    api.get<Items<Group>>("/groups").then((r) => r.items),
    api.get<Items<Host>>("/hosts").then((r) => r.items),
  ]);
  const grants = await Promise.all(
    users.map((u) => api.get<Items<Grant>>(`/users/${u.id}/grants`).then((r) => r.items)),
  );
  return { users: users.map((u, i) => ({ ...u, grants: grants[i] ?? [] })), groups, hosts };
}

// Runs a mutation with consistent notifications. Returns true on success.
function useMutation() {
  const notify = useNotify();
  const owner = useOwnerAction();
  return useCallback(
    async (
      action: () => Promise<unknown>,
      success: string,
      overrides: Partial<Record<string, string>> = {},
    ) => {
      try {
        await owner(action);
        notify.success(success);
        return true;
      } catch (failure) {
        if (!(failure instanceof ReauthCancelled)) notify.error(errorMessage(failure, overrides));
        return false;
      }
    },
    [notify, owner],
  );
}

function GrantsDialog({
  title,
  description,
  initial,
  hosts,
  onSave,
  onClose,
}: {
  title: string;
  description: string;
  initial: Grant[];
  hosts: Host[];
  onSave: (grants: Grant[]) => Promise<boolean>;
  onClose: () => void;
}) {
  const [grants, setGrants] = useState(initial);
  const [busy, setBusy] = useState(false);
  const duplicated = duplicateGrant(grants);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    if (await onSave(grants)) onClose();
    setBusy(false);
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>
          <GrantEditor idPrefix="edit-grants" value={grants} onChange={setGrants} hosts={hosts} />
          {duplicated && <ErrorText>同じロールと範囲が重複しています。</ErrorText>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              キャンセル
            </Button>
            <Button type="submit" disabled={busy || duplicated}>
              保存
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function UsersTab({ directory, reload }: { directory: Directory; reload: () => void }) {
  const me = useMe();
  const { end } = useSession();
  const confirm = useConfirm();
  const mutate = useMutation();
  const [editing, setEditing] = useState<Directory["users"][number] | null>(null);
  const amOwner = isOwner(me.grants);

  const setEnabled = async (user: User, enabled: boolean) => {
    if (
      !enabled &&
      !(await confirm({
        title: `「${user.username}」を無効にしますか？`,
        description:
          "このユーザーのすべてのセッションが終了し、ログインできなくなります。あとで有効に戻せますが、終了したセッションは復活しません。",
        confirmLabel: "無効にする",
        destructive: true,
      }))
    )
      return;
    if (
      await mutate(
        () => api.patch(`/users/${user.id}/enabled`, { enabled }),
        enabled
          ? `「${user.username}」を有効にしました。`
          : `「${user.username}」を無効にしました。`,
        { conflict: "最後の有効なオーナーは無効にできません。" },
      )
    ) {
      if (user.id === me.id && !enabled) end("自分のアカウントを無効にしました。");
      else reload();
    }
  };

  const setOwner = async (user: User, enabled: boolean) => {
    if (
      !(await confirm({
        title: enabled
          ? `「${user.username}」をオーナーにしますか？`
          : `「${user.username}」のオーナー権限を解除しますか？`,
        description: enabled
          ? "オーナーはすべての操作ができます。対象ユーザーのセッションは終了し、ログインし直す必要があります。"
          : "対象ユーザーのセッションは終了し、ログインし直す必要があります。ほかのロールはそのまま残ります。",
        confirmLabel: enabled ? "オーナーにする" : "解除する",
        destructive: !enabled,
      }))
    )
      return;
    const done = await mutate(
      () => api.put(`/users/${user.id}/owner`, { enabled }),
      enabled
        ? `「${user.username}」をオーナーにしました。`
        : `「${user.username}」のオーナー権限を解除しました。`,
      {
        conflict: enabled
          ? "無効なユーザーはオーナーにできません。"
          : "最後の有効なオーナーは解除できません。",
      },
    );
    if (done && user.id === me.id)
      end("オーナー権限が変更されました。もう一度ログインしてください。");
    else if (done) reload();
  };

  const transfer = async (user: User) => {
    if (
      !(await confirm({
        title: `オーナーを「${user.username}」に移譲しますか？`,
        description:
          "あなたのオーナー権限は解除され、全ホスト範囲のAdminになります。あなたと移譲先のセッションは終了し、どちらもログインし直す必要があります。",
        confirmLabel: "移譲する",
        destructive: true,
      }))
    )
      return;
    if (
      await mutate(
        () => api.post("/ownership/transfer", { user_id: user.id }),
        `オーナーを「${user.username}」に移譲しました。`,
        {
          conflict: "無効なユーザーや自分自身には移譲できません。",
        },
      )
    )
      end("オーナーを移譲しました。もう一度ログインしてください。");
  };

  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>ユーザー名</TableHead>
            <TableHead>状態</TableHead>
            <TableHead>直接のロール</TableHead>
            <TableHead className="text-right">操作</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {directory.users.length === 0 && (
            <TableEmpty colSpan={4}>ユーザーがいません。</TableEmpty>
          )}
          {directory.users.map((user) => {
            const owner = isOwner(user.grants);
            const others = withoutOwner(user.grants);
            return (
              <TableRow key={user.id}>
                <TableCell>
                  {user.username}
                  {user.id === me.id && (
                    <span className="ml-2 text-xs text-muted-foreground">（自分）</span>
                  )}
                </TableCell>
                <TableCell>
                  {user.enabled ? (
                    <Status tone="success">有効</Status>
                  ) : (
                    <Status tone="neutral">無効</Status>
                  )}
                </TableCell>
                <TableCell>
                  <div className="flex flex-wrap gap-1">
                    {owner && <Badge>Owner</Badge>}
                    {others.map((grant, index) => (
                      <Badge key={index} variant="outline">
                        {grantLabel(grant, directory.hosts)}
                      </Badge>
                    ))}
                    {user.grants.length === 0 && (
                      <span className="text-xs text-muted-foreground">
                        なし（グループ経由のみ）
                      </span>
                    )}
                  </div>
                </TableCell>
                <TableCell>
                  <div className="flex flex-wrap justify-end gap-1">
                    <Button size="sm" variant="outline" onClick={() => setEditing(user)}>
                      ロール
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => void setEnabled(user, !user.enabled)}
                    >
                      {user.enabled ? "無効にする" : "有効にする"}
                    </Button>
                    {amOwner && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => void setOwner(user, !owner)}
                      >
                        {owner ? "オーナー解除" : "オーナーにする"}
                      </Button>
                    )}
                    {amOwner && user.id !== me.id && user.enabled && (
                      <Button size="sm" variant="ghost" onClick={() => void transfer(user)}>
                        オーナーを移譲
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
      {editing && (
        <GrantsDialog
          title={`「${editing.username}」の直接のロール`}
          description="グループ経由のロールは「グループ」タブで編集します。オーナー権限は専用の操作で変更します。"
          initial={withoutOwner(editing.grants)}
          hosts={directory.hosts}
          onClose={() => setEditing(null)}
          onSave={async (grants) => {
            const done = await mutate(
              () => api.put(`/users/${editing.id}/grants`, { grants }),
              `「${editing.username}」のロールを更新しました。`,
            );
            if (done) reload();
            return done;
          }}
        />
      )}
    </>
  );
}

function MembersDialog({
  group,
  users,
  onClose,
  onSave,
}: {
  group: Group;
  users: User[];
  onClose: () => void;
  onSave: (ids: string[]) => Promise<boolean>;
}) {
  const [selected, setSelected] = useState(new Set(group.user_ids));
  const [busy, setBusy] = useState(false);
  const toggle = (id: string, checked: boolean) =>
    setSelected((current) => {
      const next = new Set(current);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    if (await onSave([...selected])) onClose();
    setBusy(false);
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>「{group.name}」のメンバー</DialogTitle>
            <DialogDescription>
              メンバーには、このグループのロールがすべて付与されます。
            </DialogDescription>
          </DialogHeader>
          <ul className="grid max-h-72 gap-2 overflow-y-auto">
            {users.map((user) => (
              <li key={user.id} className="flex items-center gap-2">
                <Checkbox
                  id={`member-${user.id}`}
                  checked={selected.has(user.id)}
                  onCheckedChange={(checked) => toggle(user.id, checked === true)}
                />
                <Label htmlFor={`member-${user.id}`} className="font-normal">
                  {user.username}
                  {!user.enabled && (
                    <span className="ml-1 text-xs text-muted-foreground">（無効）</span>
                  )}
                </Label>
              </li>
            ))}
          </ul>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              キャンセル
            </Button>
            <Button type="submit" disabled={busy}>
              保存
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function GroupsTab({ directory, reload }: { directory: Directory; reload: () => void }) {
  const confirm = useConfirm();
  const mutate = useMutation();
  const [name, setName] = useState("");
  const [members, setMembers] = useState<Group | null>(null);
  const [grants, setGrants] = useState<Group | null>(null);
  const username = (id: string) => directory.users.find((u) => u.id === id)?.username ?? id;

  const create = async (event: FormEvent) => {
    event.preventDefault();
    const value = name.trim().toLowerCase();
    if (!usernamePattern.test(value)) {
      setName(value);
      return;
    }
    if (
      await mutate(
        () => api.post("/groups", { name: value }),
        `グループ「${value}」を作成しました。`,
        { conflict: "同じ名前のグループがすでにあります。" },
      )
    ) {
      setName("");
      reload();
    }
  };

  const remove = async (group: Group) => {
    if (
      !(await confirm({
        title: `グループ「${group.name}」を削除しますか？`,
        description: "メンバーは、このグループから得ていたロールを失います。",
        confirmLabel: "削除する",
        destructive: true,
      }))
    )
      return;
    if (
      await mutate(
        () => api.delete(`/groups/${group.id}`),
        `グループ「${group.name}」を削除しました。`,
      )
    )
      reload();
  };

  const invalidName = name !== "" && !usernamePattern.test(name.trim().toLowerCase());
  return (
    <div className="grid gap-4">
      <form onSubmit={create} className="flex flex-wrap items-end gap-2">
        <Field id="group-name" label="新しいグループ">
          <Input
            id="group-name"
            className="w-64"
            placeholder="例: operators"
            value={name}
            onChange={(e) => setName(e.target.value)}
            aria-invalid={invalidName}
          />
        </Field>
        <Button type="submit" disabled={name.trim() === "" || invalidName}>
          <PlusIcon />
          作成
        </Button>
        {invalidName && (
          <p className="w-full text-xs text-destructive">
            3〜64文字の英小文字・数字・「_」「-」「.」で入力してください。
          </p>
        )}
      </form>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>グループ</TableHead>
            <TableHead>メンバー</TableHead>
            <TableHead>ロール</TableHead>
            <TableHead className="text-right">操作</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {directory.groups.length === 0 && (
            <TableEmpty colSpan={4}>グループはまだありません。</TableEmpty>
          )}
          {directory.groups.map((group) => (
            <TableRow key={group.id}>
              <TableCell className="font-medium">{group.name}</TableCell>
              <TableCell className="text-sm">
                {group.user_ids.length === 0 ? (
                  <span className="text-muted-foreground">なし</span>
                ) : (
                  group.user_ids.map(username).join("、")
                )}
              </TableCell>
              <TableCell>
                <div className="flex flex-wrap gap-1">
                  {group.grants.length === 0 && (
                    <span className="text-xs text-muted-foreground">なし</span>
                  )}
                  {group.grants.map((grant, index) => (
                    <Badge key={index} variant="outline">
                      {grantLabel(grant, directory.hosts)}
                    </Badge>
                  ))}
                </div>
              </TableCell>
              <TableCell>
                <div className="flex flex-wrap justify-end gap-1">
                  <Button size="sm" variant="outline" onClick={() => setMembers(group)}>
                    メンバー
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => setGrants(group)}>
                    ロール
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => void remove(group)}>
                    削除
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {members && (
        <MembersDialog
          group={members}
          users={directory.users}
          onClose={() => setMembers(null)}
          onSave={async (ids) => {
            const done = await mutate(
              () => api.put(`/groups/${members.id}/members`, { user_ids: ids }),
              `「${members.name}」のメンバーを更新しました。`,
            );
            if (done) reload();
            return done;
          }}
        />
      )}
      {grants && (
        <GrantsDialog
          title={`「${grants.name}」のロール`}
          description="グループのメンバー全員に付与されるロールです。"
          initial={grants.grants}
          hosts={directory.hosts}
          onClose={() => setGrants(null)}
          onSave={async (value) => {
            const done = await mutate(
              () => api.put(`/groups/${grants.id}/grants`, { grants: value }),
              `「${grants.name}」のロールを更新しました。`,
            );
            if (done) reload();
            return done;
          }}
        />
      )}
    </div>
  );
}

function InvitationsTab({ hosts }: { hosts: Host[] }) {
  const notify = useNotify();
  const mutate = useMutation();
  const [username, setUsername] = useState("");
  const [grants, setGrants] = useState<Grant[]>([{ role: "Viewer", all_hosts: true }]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // There is no list API; this tab shows invitations issued from this page.
  const [issued, setIssued] = useState<Invitation[]>([]);
  const [revoked, setRevoked] = useState(new Set<string>());

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const name = username.trim().toLowerCase();
    if (!usernamePattern.test(name)) {
      setError("ユーザー名は3〜64文字の英小文字・数字・「_」「-」「.」で入力してください。");
      return;
    }
    if (grants.length === 0 || duplicateGrant(grants)) {
      setError("ロールを1つ以上、重複なく指定してください。");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const invitation = await api.post<Invitation>("/invitations", { username: name, grants });
      setIssued((current) => [invitation, ...current]);
      setUsername("");
      notify.success(`「${invitation.username}」への招待を発行しました。`);
    } catch (failure) {
      setError(
        errorMessage(failure, {
          conflict: "このユーザー名はすでに使われています。",
          forbidden: "自分の権限を超えるロールは招待できません。",
        }),
      );
    } finally {
      setBusy(false);
    }
  };

  const copy = async (token: string) => {
    try {
      await navigator.clipboard.writeText(token);
      notify.success("招待トークンをコピーしました。");
    } catch {
      notify.error("コピーできませんでした。トークンを選択してコピーしてください。");
    }
  };

  const revoke = async (invitation: Invitation) => {
    if (
      await mutate(
        () => api.delete(`/invitations/${invitation.id}`),
        `「${invitation.username}」への招待を取り消しました。`,
        { conflict: "この招待はすでに使用されています。" },
      )
    ) {
      setRevoked((current) => new Set(current).add(invitation.id));
    }
  };

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>招待を発行</CardTitle>
          <CardDescription>
            ユーザー名とロールを決めて招待します。招待の有効期限は24時間で、1回だけ使えます。
          </CardDescription>
        </CardHeader>
        <form onSubmit={submit} className="grid gap-4">
          <Field
            id="invite-username"
            label="ユーザー名"
            hint="3〜64文字の英小文字・数字・「_」「-」「.」"
          >
            <Input
              id="invite-username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              required
            />
          </Field>
          <GrantEditor idPrefix="invite" value={grants} onChange={setGrants} hosts={hosts} />
          {error && <ErrorText>{error}</ErrorText>}
          <Button type="submit" className="w-fit" disabled={busy}>
            招待を発行
          </Button>
        </form>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>この画面で発行した招待</CardTitle>
          <CardDescription>
            トークンはこの画面でしか表示できません。招待した方へ安全な方法で伝え、ログイン画面の「招待を受け取った方はこちら」から登録してもらってください。
          </CardDescription>
        </CardHeader>
        {issued.length === 0 ? (
          <p className="text-sm text-muted-foreground">まだ発行していません。</p>
        ) : (
          <ul className="grid gap-3">
            {issued.map((invitation) => (
              <li key={invitation.id} className="grid gap-2 rounded-lg border p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="font-medium">{invitation.username}</span>
                  <span className="stamp">有効期限: {formatTime(invitation.expires_at)}</span>
                </div>
                {revoked.has(invitation.id) ? (
                  <Status tone="neutral">取り消し済み</Status>
                ) : (
                  <>
                    <code
                      data-testid="invitation-token"
                      className="block overflow-x-auto rounded-md bg-muted p-2 font-mono text-xs break-all select-all"
                    >
                      {invitation.token}
                    </code>
                    <div className="flex gap-2">
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => void copy(invitation.token)}
                      >
                        <CopyIcon />
                        コピー
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => void revoke(invitation)}>
                        取り消す
                      </Button>
                    </div>
                  </>
                )}
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}

export function UsersPage() {
  const { data, error, loading, reload } = useLoad(loadDirectory);
  return (
    <>
      <PageHeader
        title="ユーザーとグループ"
        description="アカウント、グループ、ロールの割り当てを管理します。変更は相手の次の操作から反映されます。"
      />
      {error && <ErrorText>{error}</ErrorText>}
      {loading && !data ? (
        <Loading />
      ) : (
        data && (
          <Tabs defaultValue="users">
            <TabsList>
              <TabsTrigger value="users">ユーザー</TabsTrigger>
              <TabsTrigger value="groups">グループ</TabsTrigger>
              <TabsTrigger value="invitations">招待</TabsTrigger>
            </TabsList>
            <TabsContent value="users">
              <UsersTab directory={data} reload={() => void reload()} />
            </TabsContent>
            <TabsContent value="groups">
              <GroupsTab directory={data} reload={() => void reload()} />
            </TabsContent>
            <TabsContent value="invitations" forceMount className="data-[state=inactive]:hidden">
              <InvitationsTab hosts={data.hosts} />
            </TabsContent>
          </Tabs>
        )
      )}
    </>
  );
}
