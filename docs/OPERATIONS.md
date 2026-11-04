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

## 初回設定とHTTPS起動

HTTPSサーバー証明書と秘密鍵を用意し、利用端末側で正しく信頼する。検証を無効にする手順は採用しない。Linuxでは専用ユーザー・0700データディレクトリ・0600秘密ファイルを使用する。WindowsのACLは別途OSで設定する必要がある。

```text
controlplane --db data/amuos.db --bootstrap-file data/setup-token
controlplane --db data/amuos.db --addr 127.0.0.1:8443 --origin https://localhost:8443 --tls-cert data/server.pem --tls-key data/server-key.pem
```

初回コマンドは未初期化DBに限りセットアップ資格情報を発行し、指定した新規ファイルに保存して終了する。平文トークンを標準出力に出さない。有効期限は10分、再発行で旧トークン無効。初期化済みDBでは拒否する。指定ファイルが既存なら上書きせず拒否する。

`POST /api/v1/setup` に資格情報・username・passwordをJSONで送信し、`Origin` を設定値に一致させる。成功後はセットアップファイルを消す。ログインでCookieとCSRFトークンを取得する。APIの詳細はAPI.md。

デフォルトはループバックのみ待受。TLS証明書・鍵がなければ起動しない。SIGTERM/割込みでは最大10秒でHTTPを停止しDBを閉じる。ログにパスワード・セッション・CSRFを出力しない。

## 未提供の運用手順

systemd導入、認証局・Agent管理、更新・移行の切戻し、整合性バックアップ・暗号化・独立環境復元は未実装。稼働中DBの単純コピーをバックアップとして扱わない。初期リリース出荷前に仕様§14の手順を実装し、両Linuxで独立環境の導入・復元試験を記録する。
