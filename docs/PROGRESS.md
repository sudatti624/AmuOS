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

- Phase 0〜2の全体ゲートは未完了。全初期リリースAPIの具体化・レビュー、Linux導入試験、Frontendと開発ツール初期化が残る。
- グループ/既存割当の編集、Ownerロールの追加・解除・移譲とその際の最後のOwner保護は未実装。グループ割当と複数Ownerの構成は試験内で作成し、ユーザー追加は招待APIで提供する。
- HTTP入口でのOrigin/JSON拒否・レート制限の監査連携、監査の保持処理、要求IDの関連付け、変更前後の詳細要約は未実装。
- ホスト確保の永続冪等キー、基盤タスク、Agentの全信頼管理、実測監視・履歴は未実装。pendingホストに実測値を表示しない。
- 管理基盤バックアップ/復元、systemd、手動更新・失敗時復旧の実装は未完了。DB再接続テストを復元テストの代わりに扱わない。
- React画面・Vite+・Tailwind・shadcn/ui・主要画面E2E、sqlc生成とgooseによる移行管理統合は未実装。
- race検証はLinux CI定義のみで、ローカル実行していない。CI自体も未実行。独立環境の復元・Linuxクリーン導入・24時間負荷・依存脆弱性レビューは未実施。

Docker管理、デプロイ、ファイル操作、VM管理、systemd変更は提供しない。実装済み基盤を本番リリース済みとして扱わない。
