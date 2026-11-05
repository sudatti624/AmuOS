# AmuOSの導入（リリース版）

リリースの配布ファイルを使って、Linuxに導入する手順です。ソースからビルドする場合は、リポジトリのREADMEを参照してください。

> [!WARNING]
> AmuOSは開発途中のソフトウェアです。停止中の暗号化バックアップ・復元を実装していますが、実環境での独立復元、systemdでの常駐、更新と切戻しの総合検証はまだ済んでいません。本番環境やインターネットに公開された環境での利用はお控えください。

## 対象環境

- Linux amd64
- 動作を確認している環境：Ubuntu 24.04、Debian 13（自動テストで確認しています）
- 必要なもの：`tar`、`sha256sum`、HTTPSの証明書と秘密鍵

## 1. ダウンロードと確認

GitHubのReleasesから、`amuos_<版>_linux_amd64.tar.gz` と `SHA256SUMS` をダウンロードし、ファイルが壊れていないか確認してから展開します。

```sh
sha256sum -c SHA256SUMS
tar -xzf amuos_<版>_linux_amd64.tar.gz
cd amuos_<版>_linux_amd64
./amuos-controlplane --version
```

## 2. データ用のディレクトリと証明書

データベースや鍵を置くディレクトリを、所有者だけが読める権限で作ります。

```sh
mkdir -m 700 data
```

接続に使う名前に合い、利用する端末が信頼できるサーバー証明書と秘密鍵を `data/server.pem` と `data/server-key.pem` に置きます。秘密鍵は `chmod 600` にしてください。証明書と鍵がないと起動しません。

手元で試すだけの場合は、次のように自己署名証明書を作れます。ブラウザに警告が出るので、試用以外には使わないでください。

```sh
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 \
  -subj /CN=localhost -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
  -keyout data/server-key.pem -out data/server.pem
chmod 600 data/server-key.pem
```

## 3. 初回設定トークンの発行

```sh
./amuos-controlplane --db data/amuos.db --bootstrap-file data/setup-token
```

トークンは `data/setup-token` に保存されます。有効期限は10分で、1回だけ使えます。標準出力には表示されません。

## 4. 起動

```sh
./amuos-controlplane --db data/amuos.db \
  --addr 127.0.0.1:8443 --origin https://localhost:8443 \
  --tls-cert data/server.pem --tls-key data/server-key.pem
```

- `--addr` は待ち受けるアドレスです。既定ではこのマシンの中からしか接続できません。
- `--origin` は、ブラウザで開くURLのOrigin（末尾の `/` なし）と完全に一致させてください。
- 停止するときは Ctrl+C を押します（SIGTERMでも停止します）。

## 5. オーナーの作成とログイン

ブラウザで `https://localhost:8443/setup` を開き、手順3のトークンと、オーナーのユーザー名・パスワード（12〜128バイト）を入力します。作成できたら `data/setup-token` を削除し、ログイン画面からログインしてください。ほかのユーザーは「ユーザーとグループ」の「招待」から招待できます。

## 更新について

停止中のバックアップと同版への復元は、同梱の [BACKUP.md](BACKUP.md) を参照してください。日次と、更新前・重要な設定変更前に取得し、本体とは別の障害領域に直近7世代以上を保管します。復旧秘密鍵はバックアップと別に保管してください。

更新元・更新先の互換性確認と、更新失敗時の切戻し手順は未整備です。起動時にDBは新しい形式へ移行されるため、移行後に古いバイナリだけを戻さないでください。更新前の版で取得したバックアップと、その版のバイナリを対にして保持します。v0.1.1以前にはこのバックアップコマンドがなく、これらの版からの更新前退避は今回の保証範囲に含みません。大切なデータはまだ置かないでください。

## ライセンス

AmuOSはApache License 2.0で公開しています（`LICENSE`）。同梱している他のソフトウェアのライセンスは `THIRD_PARTY_LICENSES.md` にあります。AmuOSのロゴはApache License 2.0の対象外です。詳しくはリポジトリのREADMEをご覧ください。
