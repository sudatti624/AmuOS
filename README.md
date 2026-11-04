# AmuOS

[![Test foundation](https://github.com/sudatti624/AmuOS/actions/workflows/test.yml/badge.svg)](https://github.com/sudatti624/AmuOS/actions/workflows/test.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

**散らばった機能を、一枚に編む。**

AmuOSは、既存のLinuxにインストールして使う、小さなサーバー管理基盤をつくろうとしているオープンソースのプロジェクトです。複数ホストの情報や監視データを、一つのURL・認証・管理画面でまとめて扱えるようにしたいと考えています。

まだ開発を始めたばかりで、いまあるのは**Go製Control Planeの認証・RBAC・監査の土台**だけです。Web画面やAgentはなく、操作はHTTP APIで行います。至らない点も多いと思いますが、少しずつ育てていければと思っています。

> [!WARNING]
> 開発途中のソフトウェアです。APIやデータ形式は予告なく変わることがあります。本番環境やインターネットに公開された環境での利用はお控えください。

仕様の詳細は [設計仕様書 v0.4](docs/SPECIFICATION.md) にまとめています。

## 名前について

AmuOSの名前は「編む」から取りました。編み物は、一本一本の糸を編み込むことで一枚の布になります。サーバー、アプリ、監視、権限など、ばらばらになりがちな管理の機能を、少しずつ編み合わせていけたら、という思いを込めています。

## いまできること

| 分野 | 実装済みの範囲 |
|---|---|
| 初回設定 | ローカルで発行する、有効期限10分・一回限りのトークンによるOwner作成 |
| 認証 | Argon2idによるパスワード保護、ログイン・ログアウト、セッションの有効期限 |
| アカウント | 期限付き招待の発行・受諾・取消、パスワード変更、ユーザー一覧・無効化・再有効化 |
| Ownerの管理 | 再認証付きの追加・解除・移譲、最後の有効Ownerの解除・無効化防止 |
| リクエスト保護 | HTTPS必須、Secure Cookie、Origin・CSRF検証、認証入口の回数・並列数制限 |
| RBAC | 固定ロールとユーザー・グループの範囲付き割当を評価する基盤、要求ごとの最新権限確認 |
| グループ管理 | グループの作成・削除、所属・固定割当・非Ownerの直接割当の編集 |
| ホスト | `pending` 状態のホストID確保と、閲覧権限のあるホストの一覧 |
| 監査 | 重要変更の原子的保存、HTTP拒否とリクエストID、範囲付き閲覧、Ownerによる保持期間設定・期限切れ削除 |
| 永続化 | SQLite、バージョン付きスキーマ適用 |

ホストIDの確保はAgent登録を意味せず、実ホストの情報・監視値はまだ取得しません。

## 初期リリースの方針

Phase 1・2の基本機能に絞り、次を優先します。

- 認証・RBAC・ユーザー管理
- Agentの安全な登録、相互認証、資格情報の更新・失効
- ホスト情報・リソース監視・履歴
- 監査ログと基盤タスクの状態管理
- 導入手順、整合性のあるバックアップ・復元、認可テスト、主要画面のE2Eテスト

Agent、監視、Web画面、バックアップ・復元、基盤タスクは今後の実装対象です。Docker管理、アプリケーションのデプロイ、ファイル管理、VM管理、systemdの変更操作は、基盤の安定性とセキュリティを検証した後のPhaseで扱います。

## 構成と技術

Control Planeが認証・認可・データ保存を担当し、各Linuxホストに配置するAgentが外向きに接続する構成です。

| 層 | 技術・状態 |
|---|---|
| Control Plane | Go 1.27系、chi v5、SQLite、slog |
| 認証 | golang.org/x/crypto/argon2、サーバー側セッション |
| Agent | Go、mTLS。未実装 |
| Web UI | React、TypeScript、Vite+、Tailwind CSS、shadcn/uiを予定。未実装 |
| 検証 | Go testing、HTTP・SQLite統合テスト。Vitest・Playwrightは今後導入 |

依存バージョンは [go.mod](go.mod)・[go.sum](go.sum) に固定しています。sqlc・gooseの統合は未完了です。

## 開発環境とビルド

Go 1.27系と、依存パッケージを取得できるネットワーク環境が必要です。以下はリポジトリ直下で実行します。

```sh
go mod download
go test ./...
go vet ./...
```

Windows / PowerShell:

```powershell
go build -o dist/controlplane.exe ./cmd/controlplane
```

Linux:

```sh
CGO_ENABLED=0 go build -o dist/controlplane ./cmd/controlplane
```

初期検証対象はLinux amd64のUbuntu Server 24.04 LTS・Debian 13です。現在確認済みなのはWindowsでのGoテスト・静的解析と、Windows / Linux amd64向けビルドです。Linuxでの導入・運用検証は未完了です。

作業用Goを `.tools/` に配置した環境の設定は [開発時の起動・運用](docs/OPERATIONS.md) を参照してください。開発ツール自体はリポジトリに含みません。

## 初回設定と起動

### 1. TLS証明書を用意する

接続先名に合い、利用端末から信頼されるサーバー証明書と秘密鍵を用意します。以下は `data/server.pem` と `data/server-key.pem` に配置した例です。TLS証明書・鍵を指定しないとサーバーは起動しません。

`data/` は事前に作成してください。Linuxでは `mkdir -m 700 data` のように所有者のみがアクセスできる権限にします。`data/` はGitの管理対象外です。

### 2. 初回設定トークンを発行する

Windows / PowerShell:

```powershell
./dist/controlplane.exe --db data/amuos.db --bootstrap-file data/setup-token
```

Linux:

```sh
./dist/controlplane --db data/amuos.db --bootstrap-file data/setup-token
```

このコマンドはトークンを指定ファイルに保存し、終了します。有効期限は10分で、未初期化DBに対してのみ実行できます。保存先が既存の場合は上書きしません。トークン・秘密鍵・DBはアクセス権を制限して保管してください。

### 3. HTTPSサーバーを起動する

Windows / PowerShell:

```powershell
./dist/controlplane.exe --db data/amuos.db --addr 127.0.0.1:8443 --origin https://localhost:8443 --tls-cert data/server.pem --tls-key data/server-key.pem
```

Linux:

```sh
./dist/controlplane --db data/amuos.db --addr 127.0.0.1:8443 --origin https://localhost:8443 --tls-cert data/server.pem --tls-key data/server-key.pem
```

デフォルトの待受先は `127.0.0.1:8443` です。`--origin` は利用するHTTPSのOriginに一致させ、末尾の `/` やパスを付けません。

### 4. APIでOwnerを作成する

`POST https://localhost:8443/api/v1/setup` に、`Content-Type: application/json` と `Origin: https://localhost:8443` を付けて、次の形式で送信します。値は実際のトークン・ユーザー名・パスワードに置き換えてください。

```json
{
  "token": "<data/setup-tokenに保存された値>",
  "username": "owner",
  "password": "<12〜128バイトのパスワード>"
}
```

成功後はトークンファイルを削除します。続いて `POST /api/v1/auth/login` でログインし、返されたCookieと `csrf_token` を利用します。認証後の変更要求には `X-CSRF-Token` が必要です。詳細は [API契約](docs/API.md) を参照してください。

## 主なAPI

| メソッド | パス | 用途 |
|---|---|---|
| GET | `/healthz` | プロセスの生存確認 |
| POST | `/api/v1/setup` | 初回Owner作成 |
| POST | `/api/v1/auth/login` | ログイン |
| POST | `/api/v1/auth/logout` | ログアウト |
| POST | `/api/v1/auth/reauthenticate` | 現在のセッションの再認証 |
| POST | `/api/v1/auth/password` | 本人のパスワード変更と全セッション失効 |
| GET | `/api/v1/users` | 管理権限を持つユーザーによるアカウント一覧 |
| PATCH | `/api/v1/users/{id}/enabled` | ユーザーの有効化・無効化 |
| GET / POST | `/api/v1/groups` | グループ一覧 / 作成 |
| PUT | `/api/v1/groups/{id}/members` | グループ所属の置換 |
| PUT | `/api/v1/groups/{id}/grants` | グループの固定割当の置換 |
| DELETE | `/api/v1/groups/{id}` | グループの削除 |
| GET / PUT | `/api/v1/users/{id}/grants` | 直接割当の参照 / 非Owner割当の置換 |
| PUT | `/api/v1/users/{id}/owner` | Owner割当の追加・解除 |
| POST | `/api/v1/ownership/transfer` | Ownerの移譲 |
| POST | `/api/v1/invitations` | 非Ownerの招待を発行 |
| POST | `/api/v1/invitations/accept` | 招待を受諾してアカウントを作成 |
| DELETE | `/api/v1/invitations/{id}` | 未使用招待を取消 |
| GET | `/api/v1/me` | 自分のID・現在の権限割当 |
| GET / POST | `/api/v1/hosts` | 認可済み一覧 / ホスト登録枠の確保 |
| GET | `/api/v1/audit` | 認可された監査イベントの閲覧 |
| GET / PUT | `/api/v1/audit/retention` | 保持期間の参照 / 再認証済みOwnerによる設定 |
| POST | `/api/v1/audit/retention/run` | 再認証済みOwnerによる期限切れログの手動削除（最大1000件） |

`/healthz` 以外の認証・認可条件、入力制約、エラーは [API契約](docs/API.md) に記載しています。Web管理画面はまだなく、トップページは提供しません。

## ドキュメント

| 資料 | 内容 |
|---|---|
| [設計仕様書](docs/SPECIFICATION.md) | 製品仕様の基準、初期リリース範囲、各Phaseの完了条件 |
| [アーキテクチャ](docs/ARCHITECTURE.md) | 構成・データモデル・信頼境界 |
| [設計判断](docs/DECISIONS.md) | 技術選定と判断理由 |
| [開発計画](docs/PLAN.md) | 実装順序と状態 |
| [進捗](docs/PROGRESS.md) | 実装・検証の証跡と未完了事項 |
| [セキュリティ](docs/SECURITY.md) | 脅威と対策・レビュー項目 |
| [運用手順](docs/OPERATIONS.md) | 開発環境・ビルド・HTTPS起動 |
| [テスト](docs/TESTING.md) | 検証計画・合格基準 |
| [OSS調査](docs/OSS_RESEARCH.md) | 上流資料と採用判断 |
| [貢献の手順](CONTRIBUTING.md) | Issue・プルリクエスト・開発ルール |

## AIの利用について

AmuOSは、AIによるコーディング支援を活用しながら開発しています。AIが書いたコードや文書をそのまま正しいものとは扱わず、テストと検証を通してから取り込むよう心がけています。それでも誤りや不自然な点が残っているかもしれません。お気づきの点があれば、Issueでお知らせいただけると助かります。

## 貢献

Issueやプルリクエストをいただけると、とても嬉しいです。不具合の報告や小さな修正、ドキュメントの誤字の指摘も大歓迎です。手順は [CONTRIBUTING.md](CONTRIBUTING.md) にまとめています。改変した内容を公開する義務はありませんが、もし共有していただけたら、ありがたく参考にさせていただきます。

## セキュリティ

脆弱性に気づかれた場合は、お手数ですが公開Issueではなく、[SECURITY.md](SECURITY.md) の方法で非公開にお知らせください。

## ライセンスと免責

AmuOSは [Apache License 2.0](LICENSE) で公開しています。ライセンスの条件の範囲で、個人・商用を問わずどなたでも自由に利用・改変・再配布していただけます。再配布の際は、ライセンス全文と著作権・帰属表示をそのまま残してください。

本ソフトウェアは「現状のまま」提供しており、明示・黙示を問わず、いかなる保証もありません。利用によって生じた不具合、データの損失、その他の損害について、適用法令で認められる範囲で、開発者は責任を負いかねます。詳しくはライセンス本文の第7条・第8条をご確認ください。

そのうえで、安定性・信頼性・セキュリティを少しずつでも良くしていけるよう努めていきます。

依存しているOSS（chi: MIT、modernc.org/sqlite: BSD-3-Clause、golang.org/x/crypto: BSD-3-Clause など）は、それぞれのライセンスに従います。素晴らしいOSSを公開してくださっている皆さまに感謝します。
