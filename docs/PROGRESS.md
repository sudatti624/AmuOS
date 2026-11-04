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
- 招待、パスワード変更、ユーザー無効化・グループ/割当管理API、Owner移譲・最後のOwner保護は未実装。テスト用の複数ユーザー・グループは試験内でのみ作成する。
- HTTP入口でのOrigin/JSON拒否・レート制限の監査連携、監査の保持処理、要求IDの関連付けは未実装。
- ホスト確保の永続冪等キー、基盤タスク、Agentの全信頼管理、実測監視・履歴は未実装。pendingホストに実測値を表示しない。
- 管理基盤バックアップ/復元、systemd、手動更新・失敗時復旧の実装は未完了。DB再接続テストを復元テストの代わりに扱わない。
- React画面・Vite+・Tailwind・shadcn/ui・主要画面E2E、sqlc生成とgooseによる移行管理統合は未実装。
- race検証はLinux CI定義のみで、ローカル実行していない。CI自体も未実行。独立環境の復元・Linuxクリーン導入・24時間負荷・依存脆弱性レビューは未実施。

Docker管理、デプロイ、ファイル操作、VM管理、systemd変更は提供しない。実装済み基盤を本番リリース済みとして扱わない。
