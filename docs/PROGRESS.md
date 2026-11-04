# 実装進捗

2026-11-04。基準: SPECIFICATION.md v0.4。初期リリース全体は未完了。

## 今回の実装

- GoモジュールとHTTPS Control Plane、chi、SQLite、バージョン付き原子的スキーマ適用。
- ローカルの10分・一回限り初回Owner設定トークン、Argon2idパスワード。
- ログイン・ログアウト、ハッシュ保存セッション、アイドル/絶対期限、Secure Cookie、Origin/CSRF、認証入口の回数・並列数制限。
- 直接/グループ割当を読み込む範囲付きRBAC。Owner制約、要求ごとの最新権限確認。
- pendingホストID確保と認可済み一覧、範囲付き監査閲覧。
- 重要な変更と監査の原子的保存、認証失敗と一部認可拒否の記録。
- 開発・設計・API・脅威モデル・運用・試験の資料とCI定義。
- OSS公開準備: Apache-2.0のLICENSE、CONTRIBUTING.md、SECURITY.md、README整備、モジュール名を `github.com/sudatti624/AmuOS` へ変更。変更後のgo fmt/vet/test・Windows/Linuxビルドを再実行しPASS（`-buildvcs=false`）。

## 検証証跡

### v0.1の配布物と導入確認（2026-11-04）

Linux amd64向けの配布物（`amuos_<版>_linux_amd64.tar.gz` と `SHA256SUMS`）を `scripts/package.sh` で作る。中身は `amuos-controlplane`（`--version` 付き）、`LICENSE`、`INSTALL.md`、`THIRD_PARTY_LICENSES.md`。後者は `scripts/licenses` がGo標準ライブラリ・リンクされるGoモジュール全件・画面に含まれるJS依存（Viteの `build.license`）のライセンス本文を集めたもの。

`scripts/smoke-install.sh` は配布物を新しいディレクトリに展開し、docs/INSTALL.mdの手順（データディレクトリ作成、証明書配置、初回トークン発行、HTTPS起動）をたどって、画面の配信、初回設定、ログイン、`/me`、ホスト登録枠の作成、再起動後のログインとデータ保持を確認する。証明書は試験用の自己署名。

| 環境 | 結果 |
|---|---|
| Windows amd64（手元） | vp check・test・build、go fmt/vet/test、Playwright 3件、配布物の作成とチェックサム確認がPASS |
| GitHub Actions run 41831048683（`7576800`） | go（ubuntu-latest / windows-latest）、web（Playwright・配布物作成）、install-ubuntu（Ubuntu 24.04ランナー）、install-debian（debian:13コンテナ）がすべて成功 |

実機のUbuntu Server・Debianでの導入、systemdでの常駐、長時間運転、バックアップ・復元は未実施（順序4・6）。手元のWindowsではWSL2が使えないため、Linuxでの確認はCIで行った。

### 順序3: Web画面と主要画面E2E（2026-11-04）

`web/` にReact 19 / TypeScript / Vite+ 1.5.0 / Tailwind v4と、shadcn/uiの方式によるRadixベースの部品で画面を追加した。概要・ホスト・アクティビティ・ユーザーとグループ（ユーザー・グループ・招待）・設定と、初回設定・ログイン・招待受諾を提供する。監視・タスクは対応APIがないため導線を置いていない。ビルド結果はembedでControl Planeに含め、応答ごとのnonce付きCSPで配信する（ADR-013）。`GET /me` に本人のusernameを追加した。

環境: Windows amd64、Go 1.27.3、Node.js 24.22.2、pnpm 12.16.1、Chromium（Playwright 1.64.1）。

| コマンド | 結果 |
|---|---|
| pnpm exec vp check（整形・lint・型） | PASS、警告0 |
| pnpm exec vp test | PASS、9件（権限表示の判定、APIクライアントのCSRF・冪等キー・401処理・エラー表示） |
| pnpm exec vp build | PASS。HTMLにインラインスクリプトなし |
| go fmt / gofmt -l cmd internal scripts / go vet / go test -count=1 ./... | PASS。画面配信・nonce・監査付き404・`/me` のusername（再起動後を含む）を追加検証 |
| Windows / Linux amd64ビルド（-buildvcs=false） | PASS |
| scripts/check-startup.ps1 | PASS |
| pnpm exec playwright test | PASS、3件。実バイナリで初回設定からパスワード変更までの主要経路を確認し、CSP違反・ページ内エラーは0件 |
| pnpm audit | 既知の脆弱性なし。実行時依存のライセンスはMIT・ISC・Apache-2.0・0BSD |

E2Eの範囲はTESTING.mdの「§11・§16.1 画面」。ホスト削除・監視履歴・切断表示・タスク状態は対応APIがないため未検証。CI（GitHub Actions、`658adf2`）でも、ubuntu-latest上でgo test・vet・race・Linuxビルド、windows-latest上でgo test・vet、webジョブでvp check・test・build・画面を含むビルド・Playwright 3件がすべて成功した。依存ライセンスの同梱は、下の「v0.1の配布物と導入確認」で対応した。

### 順序0・1の完了確認（2026-11-04）

設計・認可・障害/復旧契約をFOUNDATION_CONTRACT.md、現行全27ルートの認可をAPI_AUTHORIZATION.csv、レビューをFOUNDATION_REVIEW.mdに整理した。chiの全ルートとの一致と、全保護APIの未認証/CSRF欠落拒否を機械検証する。Go検証版を.go-versionとCIで1.27.3へ固定した。

順序1の監査詳細・自動保持・永続冪等性の残作業は実装と試験を完了した。順序3には着手していない。製品Phase 0〜2の実装全体・出荷ゲートの完了とは区別する。

最終確認はWindows amd64 / Go 1.27.3でgo fmt/test/vet、Windows/Linux amd64ビルド、実バイナリを使うscripts/check-startup.ps1がPASS。Linuxでの実行・race・画面E2E・ホスト側CIは未実施。

govulncheck v1.9.0（2026-11-04）の結果は到達シンボル0件、importするパッケージ0件。依存モジュールgolang.org/x/crypto v0.58.0内の未使用openpgpにGO-2026-5932が1件ある。本製品は当該パッケージをimportしない。指摘を未解消のモジュール情報として記録し、依存更新時とリリース時に再検査する。

以下の各検証記録の「未実装」「未実施」は、それぞれの記録時点の状態である。現在の順序0・1の判定は本節とFOUNDATION_REVIEW.mdを参照する。

### 順序1: ホスト作成の永続冪等性（2026-11-04）

スキーマv7で要求者・操作・範囲・キーハッシュの一意な操作記録を追加した。HTTPでIdempotency-Keyを必須化し、ホスト・結果参照・監査を原子的に保存する。同じ入力の再要求は元の結果を返し、異なる入力・権限剥奪・期限切れを拒否する。30日後は自動処理で結果参照を除去し、キー再利用を拒否する記録を残す。

go fmt/test/vetがPASS。同時6要求の一回作成、応答喪失を想定した再起動後の再取得、入力競合、要求者範囲の分離、権限変更後の拒否、監査失敗での操作記録/ホストのロールバック、期限切れキーの再利用拒否、HTTP応答と再要求の監査相関を検証した。

### 順序1: 監査保持の自動処理（2026-11-04）

スキーマv6に自動処理の有効性、次回・最終実行、失敗状態を追加した。起動後と毎分の確認で、通常1時間・残件1分・失敗5分の間隔で処理する。予定の取得・最大1000件削除・監査・次回予定を原子的に確定し、再起動後も予定を保持する。Ownerは再認証付き設定で自動処理を停止/再開できる。TLS資格情報と待受確立に失敗した起動ではワーカーを開始しない。

go fmt/test/vetがPASS。同時確認の一回実行、再起動時の予定保持、停止/再開、監査失敗時の削除/予定ロールバックと再試行、失敗状態の回復、ワーカーのキャンセルとエラー後の継続を検証した。

### 順序1: 変更前後の監査要約（2026-11-04）

アカウント・パスワード変更・所属・割当・招待・Owner操作にbefore/afterの明示的なフィールド投影を追加した。ユーザーのパスワードハッシュや資格情報を含むDB行を丸ごと保存しない。移譲元/先、セッション失効件数、認可バージョン、変更された所属・固定割当を確認できる。監査の生成・保存まで同じトランザクションで行い、失敗時は更新を戻す。

go fmt/test/vetがPASS。直接割当・認可バージョン、所属、Owner移譲、パスワード変更前後のセッション件数、秘密値・ハッシュの除外を追加検証した。既存の監査失敗ロールバック試験もPASS。

### 監査の保持期間と期限切れ削除（2026-11-04）

スキーマv5でaudit_settingsとイベントdetails・時刻索引を追加した。保持期間は既定180日、設定可能範囲1〜3650日。設定変更と専用の手動削除は再認証済みOwnerに限定する。削除は期限より古いイベントを最大1000件まで対象とし、境界時刻ちょうどのイベントは残す。前後の保持日数、削除基準・件数・ID範囲を操作と同じトランザクションで記録する。自動スケジューラは未実装。

Windows / Go 1.27.3でgo fmt/test/vetとWindows/Linux amd64のビルド（-buildvcs=false）がPASS。Owner再認証、Adminの変更・削除拒否、設定の上下限と再起動後保持、保持期限境界、削除の証跡、同時バッチ間の非重複、監査保存失敗時の設定/削除ロールバックと回復を検証した。HTTPでもCSRF・null本文・個別ID指定の拒否と専用処理の結果を確認した。Linux実行・race・E2Eは未実施。

### リクエストIDとHTTP拒否監査（2026-11-04）

スキーマv4で監査イベントへrequest_idと索引を追加した。サーバー生成IDを応答と業務監査に関連付け、入口のOrigin/JSON/入力/レート制限や業務エラー、404/405をhttp.rejectとして保存する。生のURL・クエリ・本文・Cookie・トークン・未検証ヘッダー値を監査に含めない。拒否の記録はセッションを延長せず、接続キャンセルから独立した最大2秒の処理とした。監査保存障害は503と秘密値を含まないログで可視化し、正常な生存確認は維持する。

Windows / Go 1.27.3でgo fmt/test/vetがPASS。IDの同時発行での一意性、成功変更と拒否のID相関、秘密値の非記録、有効主体の判定とセッション非延長、切断後の拒否監査、監査失敗時の503、生存確認の継続、再起動後のID保持を検証した。Linux実行・race・E2Eは未実施。

### Ownerの追加・解除・移譲（2026-11-04）

Owner専用APIによる追加・解除と、移譲元を全体Adminへ変更する原子的な移譲を追加した。どちらも直近5分以内の同じセッションでの再認証が必要。追加先は有効ユーザーに限定し、最後の有効Ownerの解除と自己への移譲を拒否する。Owner割当変更時は対象の全セッションを失効し、移譲では両者のセッションを失効する。

Windows / Go 1.27.3で `go fmt ./...`、`go test ./...`、`go vet ./...`、Windows/Linux amd64のビルド（`-buildvcs=false`）がPASS。Adminによる昇格、期限切れ再認証、無効ユーザーへの付与・移譲を拒否すること、同時解除でも有効Ownerを維持すること、監査失敗時に権限・版・セッションを巻き戻すこと、再起動後の移譲状態の保持を検証した。HTTPでも昇格前セッションの拒否、移譲元Cookieの削除、CSRFと未認証の拒否を確認した。Linux実行・race・CI・画面E2Eは未実施。

### グループ・固定割当の管理（2026-11-04）

グループの一覧・作成・削除、所属と固定ロールの一括置換、非Ownerの直接割当の参照・置換を追加した。Ownerを含むグループとOwnerの所属変更は、Ownerによる同一セッションの再認証を必要とする。グループ経由のOwner付与は拒否する。

スキーマv3でauthorization_versionを追加し、所属・割当・有効性の変更時に影響するユーザーの版更新、未使用発行招待の取消、再認証状態のクリアを原子的に行う。権限を戻しても以前の招待は復活しない。

Windows / Go 1.27.3で `go fmt ./...`、`go test ./...`、`go vet ./...` がPASS。直接Viewer(A)とグループAdmin(B)の範囲分離、既存セッションへの即時反映、Owner所属の保護、入れ子・重複・権限不足の拒否、監査失敗時の所属/割当/版/削除のロールバック、再起動後の保持、削除後の権限消失を検証した。HTTP統合試験もPASS。Linux実行・race・主要画面E2Eは未実施。

### アカウント管理の追加（2026-11-04）

- 招待の発行・受諾・取消を実装。24時間・一回限り・名前と割当を固定し、発行/受諾時に最新の発行者権限を検証する。トークンはハッシュ保存し、再発行・発行者無効化で旧招待を取り消す。
- パスワード変更では現在のパスワードを確認し、本人の全セッションを削除する。
- 管理権限付きのユーザー一覧、有効化・無効化APIを追加。無効化後に再有効化しても以前のセッションと発行招待は復活しない。
- 明示的な再認証APIを追加。Ownerの状態変更は当該セッションの5分以内の再認証を要求し、最後の有効Ownerの無効化を拒否する。Ownerのロール追加・解除・移譲は未実装。
- スキーマv2への原子的移行を追加。v1のユーザーとセッションを維持し、既存セッションには再認証権限を付与しない。

検証結果（Windows amd64 / Go 1.27.3）：`go fmt ./...`、`go test ./...`、`go vet ./...`、WindowsとLinux amd64のビルド（`-buildvcs=false`）はすべてPASS。Linux上での実行・race・CI・主要画面E2Eは今回も未実施。

追加試験では、招待の範囲固定・同時受諾・期限境界・取消・再発行、発行者の権限変更と無効化、対象ホスト削除、再起動後の招待受諾を確認した。パスワード変更の全セッション失効、Ownerの再認証期限・セッション分離、並行無効化時の有効Owner維持、監査失敗時のアカウント変更/招待受諾のロールバック、移行途中失敗時のスキーマ・版のロールバックも確認した。HTTP統合試験では招待受諾時の割当変更を拒否し、パスワード変更後のCookie削除と旧セッション拒否を確認した。

### 既存基盤の検証

環境: Windows amd64、公式SHA-256照合済みGo 1.27.3。依存はchi v5.5.0、modernc.org/sqlite v1.61.1、golang.org/x/crypto v0.58.0。Goキャッシュは `.cache/`、ツールチェーンは `.tools/` に置き、Git対象外。

| コマンド | 結果 |
|---|---|
| go test ./... | PASS。controlplane / rbac / security / storeが成功。cmdはテストなし |
| go vet ./... | PASS |
| gofmt -l cmd internal | 出力なし |
| go build -o dist/controlplane.exe ./cmd/controlplane | PASS（Windows） |
| CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/controlplane ./cmd/controlplane | PASS（クロスビルド、Linux上での実行は未検証） |
| git diff --check | PASS |

SQLite統合試験では、同一初回設定トークンの並行消費が一件だけ成功すること、期限切れ・CSRF不一致・無効ユーザーを拒否すること、Admin(A)+Viewer(B)でB管理不可であること、グループ離脱が既存セッションへ反映されること、監査挿入失敗でホスト追加を巻き戻すことを確認した。DBを閉じて再度開き、ホスト・セッション・監査の永続性を確認した。

## 未完了・制約

- 製品Phase 0〜2の出荷ゲートは未完了。順序0の設計・契約レビューと順序1のAPI基盤は完了したが、画面・実Agent・タスク・復旧・導入の実装/実証が残る。
- アカウント・グループ・固定割当・Owner管理はAPIで提供する。画面と主要画面E2E、権限付きリソース配信は未実装。
- 監査保持の自動処理・変更前後の要約と、ホスト作成の永続的な冪等性は実装済み。
- 基盤タスク、Agentの全信頼管理、実測監視・履歴は未実装。pendingホストに実測値を表示しない。
- 管理基盤バックアップ/復元、systemd、手動更新・失敗時復旧の実装は未完了。DB再接続テストを復元テストの代わりに扱わない。
- React画面・Vite+・Tailwind・shadcn/ui・主要画面E2E、sqlc生成とgooseによる移行管理統合は未実装。
- race検証はLinux CI定義のみで、ローカル実行していない。CI自体も未実行。独立環境の復元・Linuxクリーン導入・24時間負荷・依存脆弱性レビューは未実施。

Docker管理、デプロイ、ファイル操作、VM管理、systemd変更は提供しない。実装済み基盤を本番リリース済みとして扱わない。
