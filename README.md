<div align="center">

<img src="docs/images/logo.png" alt="AmuOSのロゴ" width="96" height="96">

# AmuOS

**散らばった機能を、一枚に編む。**

既存のLinuxに入れて使う、小さなセルフホスト型のサーバー管理基盤

[![Test foundation](https://github.com/sudatti624/AmuOS/actions/workflows/test.yml/badge.svg)](https://github.com/sudatti624/AmuOS/actions/workflows/test.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
![Status: early development](https://img.shields.io/badge/status-early_development-orange)

</div>

AmuOSは、複数のLinuxホストの情報や監視データを、一つのURL・一つのログイン・一つの管理画面でまとめて扱えるようにすることを目指すオープンソースのプロジェクトです。

まだ開発を始めたばかりで、いまあるのは認証・権限管理・監査の土台と、それを操作する最初のWeb画面です。至らない点も多いと思いますが、少しずつ育てていければと思っています。

*A small, self-hosted server management platform for existing Linux hosts. Early development — currently authentication, RBAC, audit logging and a first web UI, written in Go and React.*

<p align="center">
  <img src="docs/images/overview.png" alt="AmuOSの概要画面" width="800">
</p>

> [!WARNING]
> 開発途中のソフトウェアです。APIやデータ形式は予告なく変わることがあります。本番環境やインターネットに公開された環境での利用はお控えください。

## 目次

- [いまできること](#いまできること)
- [はじめかた](#はじめかた)
- [開発](#開発)
- [構成](#構成)
- [ロードマップ](#ロードマップ)
- [ドキュメント](#ドキュメント)
- [AIの利用について](#aiの利用について)
- [貢献](#貢献)
- [ライセンス](#ライセンス)

## いまできること

- **アカウントと権限**
  - 初回設定（ローカルで発行する10分・1回限りのトークン）でオーナーを作成
  - 期限付きの招待、パスワード変更、ユーザーの無効化と再有効化
  - Owner / Admin / Operator / Viewer の固定ロールを、全ホストまたはホスト単位で割り当て（ユーザーへ直接、またはグループ経由）
  - 権限の変更は、ログイン中のセッションにも次の操作から反映
  - オーナーの追加・解除・移譲は、パスワードの再確認つき。最後のオーナーは外せません
- **監査ログ**：重要な操作と拒否された要求を、変更前後の要約とリクエストIDつきで記録。保持期間を設定でき、期限切れのログは自動で削除
- **ホスト**：ホストの登録枠（ID）の確保と一覧。通信が切れて再送しても重複して作られません
- **Agent**（開発中の版のみ、次のリリースに含める予定です）：一回限りのコードでの登録、mTLSでの接続と接続状態の表示、資格情報の自動更新、ホストの失効・削除、ホスト情報と監視値（CPU・メモリ・ディスク・ネットワークなど）の取得、履歴グラフ、監視の保持期間・収集間隔の設定、読み取り収集タスクの配送、systemdサービス状態の表示
- **Web画面**：日本語表示、ダーク/ライトテーマ、スマートフォンの幅にも対応
- **運用**：停止中の暗号化バックアップと同じ版への復元、systemdでの常駐、再インストールなしの更新と、更新前のバックアップを使った切り戻し
- **安全のための基本**：HTTPS必須、Argon2idによるパスワード保護、Origin・CSRFの検証、ログイン試行の回数制限、厳格なContent Security Policy

**まだできないこと**：実機での長時間運転と独立した環境での復元の確認。Docker・アプリのデプロイ・ファイル管理・VM管理は、基盤が安定してから取り組む予定です。

## はじめかた

### リリース版を使う（Linux amd64）

[Releases](https://github.com/sudatti624/AmuOS/releases) から `amuos_<版>_linux_amd64.tar.gz` と `SHA256SUMS` をダウンロードし、[導入手順](docs/INSTALL.md) に沿って進めてください。展開して、証明書を置き、初回設定トークンを発行して起動するだけで試せます。

### ソースからビルドする

必要なものは Go 1.27系、Node.js 24系、pnpm 12系です。

```sh
git clone https://github.com/sudatti624/AmuOS.git
cd AmuOS

# 1. Web画面をビルド（Goのバイナリに組み込まれるので、先にビルドします）
cd web && pnpm install && pnpm exec vp build && cd ..

# 2. Control Planeをビルド
go build -o dist/controlplane ./cmd/controlplane

# 3. 初回設定トークンを発行（data/setup-token に保存され、10分間有効）
mkdir -m 700 data
./dist/controlplane --db data/amuos.db --bootstrap-file data/setup-token

# 4. HTTPSで起動（証明書と秘密鍵は事前に用意します）
./dist/controlplane --db data/amuos.db \
  --addr 127.0.0.1:8443 --origin https://localhost:8443 \
  --tls-cert data/server.pem --tls-key data/server-key.pem
```

ブラウザで `https://localhost:8443/setup` を開き、手順3のトークンと、オーナーのユーザー名・パスワードを入力します。作成できたら、トークンのファイルを削除してログインしてください。ほかのユーザーは「ユーザーとグループ」の「招待」から招待できます。

- 証明書は、接続先の名前に合い、利用する端末が信頼できるものを使ってください。TLSの証明書と鍵がないと起動しません。
- `--origin` は、ブラウザで開くURLのOrigin（末尾の `/` なし）と完全に一致させます。
- Windowsでの手順や詳しい運用は [運用手順](docs/OPERATIONS.md) にあります。APIを直接使う場合は [API契約](docs/API.md) を参照してください。

## 開発

```sh
# Web画面（web/ で実行）
pnpm exec vp check     # 整形・lint・型チェック
pnpm exec vp test      # 単体テスト
pnpm exec vp build

# Control Plane（リポジトリ直下で実行）
go test ./...
go vet ./...

# ブラウザでのE2Eテスト（ビルド済みのバイナリを使います）
go build -o dist/controlplane ./cmd/controlplane
go build -o dist/devcert ./scripts/devcert    # テスト専用の自己署名証明書ツール
cd web && pnpm exec playwright install chromium && pnpm exec playwright test
```

E2Eテストは、テストごとに新しいデータベースと証明書でControl Planeを起動し、初回設定から権限の変更、APIを直接呼んだときの拒否までを確認します。開発の進め方は [CONTRIBUTING.md](CONTRIBUTING.md) にまとめています。

## 構成

Control Planeが認証・認可・データの保存を担当し、将来は各Linuxホストに置くAgentがControl Planeへ外向きに接続する構成です。Web画面はControl Planeのバイナリに組み込まれ、同じURLで配信されます。

| 部分 | 技術 |
|---|---|
| Control Plane | Go 1.27、chi、SQLite（modernc.org/sqlite）、Argon2id |
| Web UI | React、TypeScript、Vite+、Tailwind CSS v4、Radixベースの部品（shadcn/uiの方式） |
| テスト | Go testing、Vitest、Playwright |
| Agent | Go、mTLS（未実装） |

対象環境はLinux amd64（Ubuntu Server 24.04 LTS・Debian 13）です。自動テスト（GitHub Actions）では、Ubuntu 24.04 上でのテストとE2Eに加えて、配布用ファイルを Ubuntu 24.04 と Debian 13 に手順どおり導入して起動できることを確認しています。Ubuntu 24.04 ではsystemdのサービスとして導入し、更新と切り戻しができること、そこで取ったバックアップを Debian 13 の新しい環境へ復元できることも確認しています。実機での長時間の運用の検証はこれからです。

## ロードマップ

| 版 | 目安 |
|---|---|
| v0.1 | 画面からユーザーと権限を管理でき、Ubuntu Server / Debian に手順どおり導入して試せる |
| v0.2 | systemdでの常駐、バックアップと復元、再インストールなしの更新と切り戻し |
| v0.3 | Agentを登録し、ホストの情報・監視値・履歴を画面で確認できる |
| v0.4 | Windowsへの対応（Control PlaneとAgent）。WSLや仮想マシンを使えない環境でも試せるようにしたいと考えています |
| v1.0 | 初期リリースの条件（独立環境での復元、認可の総合検証、24時間の連続運転など）をすべて満たす |

v1.0のあとには、MCP（Model Context Protocol）への対応も検討しています。AIアシスタントから、ご自身の権限の範囲でホストの状態や監査ログを確認できるようにしたいと考えています。

詳しくは [開発計画](docs/PLAN.md) と [設計仕様書](docs/SPECIFICATION.md) をご覧ください。

## ドキュメント

| 資料 | 内容 |
|---|---|
| [設計仕様書](docs/SPECIFICATION.md) | 製品の仕様、初期リリースの範囲、完了条件 |
| [アーキテクチャ](docs/ARCHITECTURE.md) | 構成、データモデル、信頼境界 |
| [設計判断](docs/DECISIONS.md) | 技術の選定と判断の理由（ADR） |
| [API契約](docs/API.md)・[API認可表](docs/API_AUTHORIZATION.csv) | エンドポイント、入力、エラー、認可の条件 |
| [セキュリティ設計](docs/SECURITY.md) | 脅威と対策 |
| [運用手順](docs/OPERATIONS.md) | ビルド、起動、開発サーバー |
| [テスト](docs/TESTING.md)・[進捗](docs/PROGRESS.md) | 検証計画と、検証の記録・未完了の事項 |

## 名前について

AmuOSの名前は「編む」から取りました。編み物は、一本一本の糸を編み込むことで一枚の布になります。サーバー、アプリ、監視、権限など、ばらばらになりがちな管理の機能を、少しずつ編み合わせていけたら、という思いを込めています。

## AIの利用について

AmuOSは、AIによるコーディング支援を活用しながら開発しています。AIが書いたコードや文書をそのまま正しいものとは扱わず、テストと検証を通してから取り込むよう心がけています。それでも誤りや不自然な点が残っているかもしれません。お気づきの点があれば、Issueでお知らせいただけると助かります。

## 貢献

Issueやプルリクエストをいただけると、とても嬉しいです。不具合の報告や小さな修正、ドキュメントの誤字の指摘も大歓迎です。手順は [CONTRIBUTING.md](CONTRIBUTING.md) にまとめています。改変した内容を公開する義務はありませんが、もし共有していただけたら、ありがたく参考にさせていただきます。

脆弱性に気づかれた場合は、お手数ですが公開Issueではなく、[SECURITY.md](SECURITY.md) の方法で非公開にお知らせください。

## ライセンス

AmuOSは [Apache License 2.0](LICENSE) で公開しています。ライセンスの条件の範囲で、個人・商用を問わずどなたでも自由に利用・改変・再配布していただけます。再配布の際は、ライセンス全文と著作権・帰属表示をそのまま残してください。

本ソフトウェアは「現状のまま」提供しており、明示・黙示を問わず、いかなる保証もありません。利用によって生じた不具合、データの損失、その他の損害について、適用法令で認められる範囲で、開発者は責任を負いかねます。詳しくはライセンス本文の第7条・第8条をご確認ください。そのうえで、安定性・信頼性・セキュリティを少しずつでも良くしていけるよう努めていきます。

### ロゴについて

AmuOSのロゴ（`docs/images/logo.png`、`web/public/favicon.png`、`web/src/assets/logo.png`）は、Apache License 2.0の対象外です。

- AmuOSを紹介したり、AmuOSへのリンクとして使ったりする場合は、ロゴを改変せずに使っていただいて構いません。
- ロゴを改変して使うこと、AmuOSと関係のない製品やサービスに使うこと、AmuOSの開発者が公認・提携しているように見える使い方は、ご遠慮ください。
- AmuOSを改変して配布する場合は、本家と見分けがつくよう、ロゴを差し替えていただけると助かります。

AmuOSは、chi、modernc.org/sqlite、golang.org/x/crypto、React、Radix UI、Tailwind CSS、Vite+ など多くのオープンソースソフトウェアに支えられています。それぞれのライセンスに従って利用しており、素晴らしいソフトウェアを公開してくださっている皆さまに感謝します。
