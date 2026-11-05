# 開発時の起動・運用

現状は認証・認可・監査基盤の開発版。初期リリースの導入・復元条件はまだ未達。

## 必要環境

Go 1.27系。ローカルに用意したGoを使う場合は `.tools/go/bin` をPATHに追加する。GOCACHE/GOMODCACHEは書込み可能な場所へ設定する。依存版はgo.mod/go.sum。

```powershell
$env:PATH = "$PWD/.tools/go/bin;$env:PATH"
$env:GOCACHE = "$PWD/.cache/go-build"
$env:GOMODCACHE = "$PWD/.cache/go-mod"
go test ./...
go vet ./...
go build -o dist/controlplane.exe ./cmd/controlplane
```

Linux amd64向けバイナリは `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/controlplane ./cmd/controlplane`。クロスビルド成功だけではLinux運用検証の代替にならない。

## Web画面のビルドと開発

Node.js 24系とpnpm 12系（`web/package.json` のpackageManager）を使う。画面はGoのバイナリに組み込むため、Goのビルドより先に `web/` でビルドする。出力先 `internal/webui/static/app` はGit対象外。

```powershell
cd web
pnpm install
pnpm exec vp check
pnpm exec vp test
pnpm exec vp build
cd ..
go build -o dist/controlplane.exe ./cmd/controlplane
```

画面を変更しながら確認する場合は、Vite+の開発サーバーをHTTPSで起動し、APIをControl Planeへ中継する。Control Planeの `--origin` は開発サーバーのOrigin（例 `https://localhost:5173`）に合わせる。証明書には利用端末が信頼するものを使う。

```powershell
$env:AMUOS_DEV_TLS_CERT = "data/dev.pem"
$env:AMUOS_DEV_TLS_KEY = "data/dev-key.pem"
$env:AMUOS_DEV_CONTROL_PLANE = "https://127.0.0.1:8443"
pnpm exec vp dev
```

リリース版の導入手順は [INSTALL.md](INSTALL.md)。配布物は Linux 上で `bash scripts/package.sh <版>` で作る（画面のビルド後）。

E2Eは `go build -o dist/devcert.exe ./scripts/devcert` でテスト用の証明書ツールも用意し、`web/` で `pnpm exec playwright install chromium` の後に `pnpm exec playwright test` を実行する。テストは実バイナリを一時ディレクトリの新しいDB・自己署名証明書で起動し、終了後に削除する。`devcert` の証明書は試験専用で、運用には使わない。

## 初回設定とHTTPS起動

HTTPSサーバー証明書と秘密鍵を用意し、利用端末側で正しく信頼する。検証を無効にする手順は採用しない。Linuxでは専用ユーザー・0700データディレクトリ・0600秘密ファイルを使用する。WindowsのACLは別途OSで設定する必要がある。

```text
controlplane --db data/amuos.db --bootstrap-file data/setup-token
controlplane --db data/amuos.db --addr 127.0.0.1:8443 --origin https://localhost:8443 --tls-cert data/server.pem --tls-key data/server-key.pem
```

初回コマンドは未初期化DBに限りセットアップ資格情報を発行し、指定した新規ファイルに保存して終了する。平文トークンを標準出力に出さない。有効期限は10分、再発行で旧トークン無効。初期化済みDBでは拒否する。指定ファイルが既存なら上書きせず拒否する。

ブラウザで `<origin>/setup` を開き、資格情報・username・passwordを入力する。APIを直接使う場合は `POST /api/v1/setup` にJSONで送信し、`Origin` を設定値に一致させる。成功後はセットアップファイルを消す。ログインでCookieとCSRFトークンを取得する。APIの詳細はAPI.md。

デフォルトはループバックのみ待受。TLS証明書・鍵がなければ起動しない。SIGTERM/割込みでは最大10秒でHTTPを停止しDBを閉じる。ログにパスワード・セッション・CSRFを出力しない。

通常起動と初回資格情報の発行は、DBを開く前にDBの親ディレクトリの `.amuos.lock` を排他ロックする。同じディレクトリを使用中なら、待機せず `state directory is already in use` で終了する。DB名を変えても同じディレクトリでの同時起動はできない。DBには通常のファイルパスを指定し、`:memory:` やSQLiteの `file:` URIはCLIでは使用しない。

Linuxではflock、Windows開発環境ではLockFileExを使用し、正常終了・強制終了のどちらでもOSがロックを解放する。ファイルが残っていても次回起動は可能。使用中のロックファイルを削除・置換すると排他が成立しなくなるため、残したまま運用する。状態は専用ユーザーが管理するローカルファイルシステムに置き、ネットワーク共有や、別ディレクトリから同じDBを指すリンクは使用しない。Windowsのファイル保護にはOSのACL設定が必要。

## アカウント管理

ログイン後、全体のアカウント管理権限を持つユーザーは `POST /api/v1/invitations` で招待を作成できる。返されるtokenは一度だけ表示され、有効期限は24時間。安全な経路で本人へ渡す。受諾者は `POST /api/v1/invitations/accept` にtokenとpasswordを送り、その後ログインする。名前とロール・範囲は招待発行時に固定される。入力形式はAPI.md。

`POST /api/v1/auth/password` の成功後は自分の全セッションが失効するため、新しいパスワードでログインし直す。`PATCH /api/v1/users/{id}/enabled` で無効化したユーザーは、再有効化しても以前のセッションを再利用できない。

Ownerの状態変更前には `POST /api/v1/auth/reauthenticate` で現在のパスワードを確認する。同じセッションで5分以内に操作する必要があり、最後の有効Ownerは解除・無効化できない。

グループは `/api/v1/groups`、所属・割当は対応するPUT APIで管理する。空配列は明示的な解除として扱い、本文の省略・nullは拒否する。所属・割当・有効性を変更すると影響するユーザーの未使用招待と再認証は取り消される。Ownerを含むグループの変更にはOwnerの再認証が必要。

Owner割当は `PUT /api/v1/users/{id}/owner`、移譲は `POST /api/v1/ownership/transfer` を使用する。移譲元は全体Adminとなり、移譲先はOwnerとなる。変更対象の全セッションが失効するため、両者はログインし直す。移譲先は有効な既存ユーザーに限る。

新しいバイナリの起動時に既存のスキーマv1〜v7をv8へ自動移行する。失敗時は版とスキーマ変更を戻す。移行後のDBに旧バイナリだけを戻す更新手順は提供しない。

`POST /api/v1/hosts` はIdempotency-Keyが必須。操作ごとに乱数キーを作り、応答を失ったときは同じキー・同じnameで再要求する。再取得にはIdempotency-Replayed: trueが付く。別入力・期限切れの409でキーを勝手に取り替えて自動再作成しない。

## 監査ログの照会と保持

応答のX-Request-IDで操作と監査イベントを関連付けられる。HTTP拒否も記録されるが、本文やCookie、実際のURL内IDなどの秘密値は記録しない。audit_unavailableが返った場合は監査保存障害を示す。サーバーログには照合用のリクエストIDを出し、内部DBエラーを利用者へそのまま返さない。

保持期間と自動処理の状態は `GET /api/v1/audit/retention` で確認する。既定180日・自動有効。変更はOwnerが再認証した後、`PUT /api/v1/audit/retention` に `{"retention_days":180,"automatic_enabled":true}` の形式で送信する。falseで自動処理を停止する。設定変更の要求自体では削除せず、次の定期確認から新しい設定を適用する。

自動処理は起動後と毎分に予定を確認し、通常1時間・残件1分・失敗5分の間隔で最大1000件を処理する。状態のlast_run_at・next_run_at・failure_count・last_error_codeで実行と障害を確認できる。機密値を含み得る低水準エラーは状態に保存しない。

手動削除は再認証済みOwnerが `POST /api/v1/audit/retention/run` へ `{}` を送って実行する。1回最大1000件。応答のmore_expiredで残件有無を確認し、追加実行する場合も結果を確認してから行う。手動の変更と削除にはOrigin・CSRFが必要。任意IDの個別削除は提供しない。

## 未提供の運用手順

停止中の暗号化バックアップ・同版復元・復旧鍵生成の手順は [BACKUP.md](BACKUP.md)。通常起動と同じ排他を使用し、復元時には旧セッション・未使用招待・初回資格情報を無効化する。ローカル保守状態はスキーマv8のlocal_maintenanceに保存し、プロセス中断は次回起動時にunknownとして監査する。

systemd導入、認証局・Agent管理、更新・移行の切戻し手順は未実装。両Linuxの独立環境復元・実容量枯渇・電源断・性能目標は未検証。稼働中DBの単純コピーをバックアップとして扱わない。
