# AmuOSの導入（リリース版）

リリースの配布ファイルを使って、Linuxに導入する手順です。ソースからビルドする場合は、リポジトリのREADMEを参照してください。

> [!WARNING]
> AmuOSは開発途中のソフトウェアです。systemdでの常駐、更新と切り戻しは自動テストの環境で確認していますが、実機での長時間運転や独立した環境での復元はまだ確認できていません。本番環境やインターネットに公開された環境での利用はお控えください。

## 対象環境

- Linux amd64
- 動作を確認している環境：Ubuntu 24.04、Debian 13（自動テストで確認しています。systemdでの導入・更新・切り戻しはUbuntu 24.04で確認しています）
- 必要なもの：`tar`、`sha256sum`、systemd、HTTPSの証明書と秘密鍵

以下のコマンドは、とくに断りがなければ管理者（root）として実行します。

## 1. ダウンロードと確認

GitHubのReleasesから、`amuos_<版>_linux_amd64.tar.gz` と `SHA256SUMS` をダウンロードし、ファイルが壊れていないか確認してから展開します。

```sh
sha256sum -c SHA256SUMS
tar -xzf amuos_<版>_linux_amd64.tar.gz
cd amuos_<版>_linux_amd64
./amuos-controlplane --version
```

## 2. サービスとして導入する

専用のユーザーを作り、ファイルを役割ごとに配置します。

| 場所 | 内容 |
|---|---|
| `/usr/local/bin/amuos-controlplane` | 本体 |
| `/etc/amuos/controlplane.env` | 待ち受けアドレスとOrigin |
| `/etc/amuos/tls/` | サーバー証明書と秘密鍵（rootだけが読めます） |
| `/var/lib/amuos/` | データベースなどの状態（専用ユーザーだけが読めます） |

```sh
useradd --system --home-dir /var/lib/amuos --no-create-home --shell /usr/sbin/nologin amuos
install -m 755 amuos-controlplane /usr/local/bin/amuos-controlplane
install -d -m 755 /etc/amuos
install -d -m 700 /etc/amuos/tls
install -m 644 packaging/controlplane.env /etc/amuos/controlplane.env
install -m 644 packaging/amuos-controlplane.service /etc/systemd/system/amuos-controlplane.service
install -d -m 700 -o amuos -g amuos /var/lib/amuos
systemctl daemon-reload
```

`/etc/amuos/controlplane.env` を開き、次の2つを環境に合わせます。

- `AMUOS_ADDR`：待ち受けるアドレスです。既定の `127.0.0.1:8443` では、このマシンの中からしか接続できません。
- `AMUOS_ORIGIN`：ブラウザで開くURLのOrigin（末尾の `/` なし）と完全に一致させてください。

接続に使う名前に合い、利用する端末が信頼できるサーバー証明書と秘密鍵を `/etc/amuos/tls/server.pem` と `/etc/amuos/tls/server-key.pem` に置き、秘密鍵は `chmod 600` にします。秘密鍵はrootのものにしたままで構いません。サービスの起動時に、systemdがサービス専用の領域へ写して渡します。

手元で試すだけの場合は、次のように自己署名証明書を作れます。ブラウザに警告が出るので、試用以外には使わないでください。

```sh
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 \
  -subj /CN=localhost -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
  -keyout /etc/amuos/tls/server-key.pem -out /etc/amuos/tls/server.pem
chmod 600 /etc/amuos/tls/server-key.pem
```

1024番未満のポート（443など）で待ち受けたい場合は、サービスに追加の権限が必要になります。いまの設定では権限を何も与えていないため、8443番などをお使いいただくか、前段のリバースプロキシで受けることをおすすめします。

## 3. 初回設定トークンの発行

専用ユーザーとして実行します。

```sh
runuser -u amuos -- /usr/local/bin/amuos-controlplane \
  --db /var/lib/amuos/amuos.db --bootstrap-file /var/lib/amuos/setup-token
```

トークンは `/var/lib/amuos/setup-token` に保存されます。有効期限は10分で、1回だけ使えます。標準出力には表示されません。

## 4. 起動

```sh
systemctl enable --now amuos-controlplane
systemctl status amuos-controlplane
```

ログは `journalctl -u amuos-controlplane` で確認できます。サービスを止めたり再起動したりしても、止まるのはAmuOS自身だけです。

## 5. オーナーの作成とログイン

ブラウザで `<AMUOS_ORIGINの値>/setup` を開き、手順3のトークン（`cat /var/lib/amuos/setup-token`）と、オーナーのユーザー名・パスワード（12〜128バイト）を入力します。作成できたら `/var/lib/amuos/setup-token` を削除し、ログイン画面からログインしてください。ほかのユーザーは「ユーザーとグループ」の「招待」から招待できます。

## バックアップ

停止中のバックアップと同版への復元の詳細は、同梱の [BACKUP.md](BACKUP.md) を参照してください。日次と、更新前・重要な設定変更前に取得し、本体とは別の障害領域に直近7世代以上を保管します。

復旧鍵は最初に一度だけ作ります。秘密鍵はAmuOSのサーバーとは別の、安全な場所に保管してください。表示される `age1...` の公開鍵を控えておきます。

```sh
install -d -m 700 /root/amuos-recovery
/usr/local/bin/amuos-controlplane --generate-recovery-key /root/amuos-recovery/recovery.key
```

バックアップはサービスを止めてから、専用ユーザーとして取得します。

```sh
install -d -m 700 -o amuos -g amuos /var/backups/amuos
systemctl stop amuos-controlplane
. /etc/amuos/controlplane.env
runuser -u amuos -- /usr/local/bin/amuos-controlplane --db /var/lib/amuos/amuos.db \
  --backup-out /var/backups/amuos/amuos-$(date +%Y%m%d-%H%M%S).age \
  --recipient age1... --addr "$AMUOS_ADDR" --origin "$AMUOS_ORIGIN"
systemctl start amuos-controlplane
```

## 更新

再インストールは必要ありません。データ・設定・証明書はそのままに、本体だけを差し替えます。

1. 新しい版の配布ファイルを手順1と同じように確認・展開します。
2. サービスを止め、上の「バックアップ」の手順で更新前のバックアップを取ります。
3. いま使っている本体を、切り戻し用に残します。バックアップと同じ版の本体でなければ復元できないため、バックアップと対にして保管してください。

   ```sh
   install -m 700 -o amuos -g amuos /usr/local/bin/amuos-controlplane \
     /var/backups/amuos/amuos-controlplane.previous
   ```

4. 新しい本体で、いまのデータベースを使えるか確かめます。この確認ではデータベースに何も書き込みません。

   ```sh
   runuser -u amuos -- ./amuos-controlplane --db /var/lib/amuos/amuos.db --check-db
   ```

   `migration_required` が `true` の場合は、起動時にデータベースが新しい形式へ移行されます。データベースのほうが新しい場合や、壊れている場合は失敗するので、そのときは差し替えずに元の版で起動し直してください。

5. 本体を差し替えて起動し、ログインできること、ホストや監査ログが残っていることを確かめます。

   ```sh
   install -m 755 amuos-controlplane /usr/local/bin/amuos-controlplane
   systemctl start amuos-controlplane
   ```

`packaging/` の設定ファイルが新しい版で変わっている場合は、リリースノートでお知らせします。

## 切り戻し

更新後に問題があった場合は、更新前のバックアップと、そのとき残した本体の組で戻します。データベースが新しい形式へ移行されたあとに、古い本体だけを戻すことはできません。バックアップ以降の変更は失われ、ログイン中のセッションと未使用の招待は無効になります。

```sh
systemctl stop amuos-controlplane
install -m 755 /var/backups/amuos/amuos-controlplane.previous /usr/local/bin/amuos-controlplane
install -d -m 700 -o amuos -g amuos /run/amuos-restore
install -m 600 -o amuos -g amuos <保管している復旧秘密鍵> /run/amuos-restore/recovery.key
runuser -u amuos -- /usr/local/bin/amuos-controlplane --db /var/lib/amuos/amuos.db \
  --restore-from /var/backups/amuos/<更新前のバックアップ>.age \
  --recovery-key-file /run/amuos-restore/recovery.key
rm -rf /run/amuos-restore
systemctl start amuos-controlplane
```

オーナーのパスワードでログインし直し、ユーザー・権限・ホスト・監査ログを確認してから利用を再開してください。復元前のデータベースは `/var/lib/amuos/.pre-restore-*` に残ります。自動では削除しないので、確認が済んだら保護したうえで扱いを決めてください。

v0.1.1以前にはバックアップのコマンドがありません。これらの版からの更新は、更新前のバックアップと切り戻しの保証範囲に含まれません。大切なデータはまだ置かないでください。

## アンインストール

サービスと本体を取り除いても、データ・設定・証明書は残ります。

```sh
systemctl disable --now amuos-controlplane
rm /etc/systemd/system/amuos-controlplane.service /usr/local/bin/amuos-controlplane
systemctl daemon-reload
```

データも消す場合は、必要なバックアップがあることを確かめてから、`/var/lib/amuos`・`/etc/amuos`・`/var/backups/amuos` と専用ユーザー（`userdel amuos`）を削除します。

## サービスにせずに試す

systemdを使わずに、手元のディレクトリで一時的に動かすこともできます。

```sh
mkdir -m 700 data
# data/server.pem と data/server-key.pem（chmod 600）を用意します
./amuos-controlplane --db data/amuos.db --bootstrap-file data/setup-token
./amuos-controlplane --db data/amuos.db \
  --addr 127.0.0.1:8443 --origin https://localhost:8443 \
  --tls-cert data/server.pem --tls-key data/server-key.pem
```

Ctrl+C（またはSIGTERM）で停止します。

## ライセンス

AmuOSはApache License 2.0で公開しています（`LICENSE`）。同梱している他のソフトウェアのライセンスは `THIRD_PARTY_LICENSES.md` にあります。AmuOSのロゴはApache License 2.0の対象外です。詳しくはリポジトリのREADMEをご覧ください。
