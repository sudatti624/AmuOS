# 順序0・1の完了確認

2026-11-04。基準はSPECIFICATION.md v0.4、対象はPLAN.mdの順序0（設計・環境・資料）と順序1（Go API基盤）。順序3の画面/E2Eには着手していない。ここでの完了は製品全体のPhase 1や初期リリース出荷を意味しない。

## 順序0: 設計レビュー

| 確認対象 | 確定した内容・参照 | 判定 |
|---|---|---|
| 対応OS・技術・開発環境 | FOUNDATION_CONTRACT.md、.go-version、go.mod/go.sum。Windows Go 1.27.3で検証しLinux amd64もクロスビルド。OSのクリーン導入は順序4/6で実証 | 設計・現在のビルド環境を確認 |
| 信頼境界とデータモデル | ARCHITECTURE.md、SECURITY.md、移行SQL。Agent/証明書/登録要求、タスク/試行、バックアップ形式もFOUNDATION_CONTRACT.mdに具体化 | 設計確定 |
| 全APIの認可・範囲 | API_AUTHORIZATION.csvに現行27ルート。ルート全件照合、全session APIの未認証とCSRF欠落の拒否試験。後続API/配信/ローカル操作も契約表で定義 | 現行実装と設計を照合 |
| 障害・安全性 | 登録競合、失効、応答喪失、DB/監査保存失敗、時刻ずれ、容量不足、鍵障害の状態/エラーを固定 | 設計確定、現行コードの障害注入も確認 |
| タスクの結果不明 | queued/dispatching/running/終端/unknown、期限と試行回数、照会、再実行禁止、永続化項目を固定 | 設計確定、実装は順序4/5 |
| バックアップ/復元 | age形式、鍵・manifest・整合性境界・展開上限、退避/切替、旧資格情報拒否・CA再発行・タスク保留を固定 | 設計確定、復元試験は順序4/6 |
| 更新 | ADR-009。再インストールなし、状態領域分離、更新前退避、前進移行、旧版+バックアップ一式で復旧 | 設計確定、配布実装は順序4 |

順序0で曖昧だった転送方式・信頼管理・データ/期限/サイズ制約・操作と範囲・復元形式は、具体的な契約に置き換えた。実装前の選択待ちとして残さず、変更時はADRと対応する試験を更新する。

## 順序1: 実装と証跡

| 要件 | 実装 | 正常・異常・認可・再開の証跡 |
|---|---|---|
| SQLite・移行 | store.Open、移行SQL | TestMigrationPreservesExistingSession / TestFailedMigrationRollsBackSchema、DB再接続後のユーザー・セッション・監査保持 |
| 初回Owner・認証 | Setup / Login / Logout / authenticate | 一回限り・同時消費、期限・無効ユーザー、CSRF、セッション期限・ログアウトの拒否 |
| 範囲付きRBAC | Principal.Allows / CanDelegate、要求ごとの再読込 | Admin(A)+Viewer(B)の分離、グループ離脱・割当変更の即時反映、Owner専用制約 |
| ホスト台帳 | CreateHost / Hosts | 閲覧範囲、作成時認可、再起動保持。実ホスト情報・Agent登録は順序5 |
| 監査の相関と拒否 | request_id / RecordHTTPRejection | ヘッダー偽装・秘密値除外、入口拒否、キャンセル時の記録、保存障害503 |
| 変更前後の要約 | auditSnapshot / auditWithDetails | TestMutationSnapshotsDescribeChangesWithoutCredentials / TestGroupAndOwnershipSnapshots、監査保存失敗のロールバック、破損detailsの拒否 |
| 監査自動保持 | RunAuditMaintenance / maintenance.Run | 予定の再起動保持、同時確認一回、設定停止/再開、監査失敗時の削除/予定ロールバックと再試行、終了時キャンセル |
| 永続冪等性 | idempotency_records / CreateHost | 同時6要求一回、再起動後の同じ結果、入力競合、要求者分離、再認可、原子的保存、30日境界と期限切れ拒否 |
| 全ルートの保護 | TestAuthorizationContractCoversEveryRoute | 文書と実装の全件一致、公開経路の固定、全保護APIの未認証・CSRF欠落の拒否、負の試験で業務状態が変わらないこと |

## 今回のレビューで確認・修正した点

- 監査にDB行全体を渡さず、許可したフィールドだけを投影する。拒否時は対象のbefore/afterを返さない。生成・保存が失敗した場合も更新を戻す。
- 保持の削除と証跡・予定を同じトランザクションにする。systemの権限を期限処理に限定し、設定更新やユーザー権限の新規付与へ拡張しない。
- 冪等キーをリクエストIDと分離し、再要求でも現在の認可を確認する。結果期限後にキーを再実行可能にせず、拒否用のハッシュを残す。
- 機密値を含み得る低水準のエラーを応答・自動処理ログへ転記しない。破損した監査JSONを200の途中まで出力しない。
- 未登録API・任意コマンド・ファイル/VM/コンテナ操作は公開しない。後続機能の設計済みと実装済みを区別する。

## 検証環境と判定の限界

ローカルWindows amd64 / Go 1.27.3で `go fmt ./...`、`go test ./...`、`go vet ./...`、Windows/Linux amd64のビルドを行う。結果はPROGRESS.mdに記録する。CIは.go-versionで同じGo版を指定するが、今回の変更に対するホスト側CIの実行結果はまだない。

CLIのTLS欠落/不正時の状態非作成、ローカル初回設定、トークン上書き拒否は、実際のWindowsビルドを使うscripts/check-startup.ps1で確認した。追加検討したcmd用Goテスト実行ファイルはこのホストのApplication Controlでブロックされたため、同じ確認を実バイナリのスモーク試験に置き換えた。スクリプト実行は承認された子プロセス内だけで許可し、ホストの恒久的な保護設定は変更していない。

本書はコード・契約・試験の照合記録であり、第三者によるセキュリティ監査の実施記録ではない。Linux実行、独立環境の導入・更新・復元、実Agent、24時間負荷、画面/E2Eは順序3〜6で実施する。これらを順序0/1の残作業として数えたり、実施済みと扱ったりしない。

最終のgo fmt/test/vet、Windows/Linux amd64ビルド、Windows実バイナリの起動スモーク試験はPASS。govulncheck v1.9.0は到達シンボル・importパッケージの脆弱性0件。依存モジュールx/crypto v0.58.0には未使用openpgpのGO-2026-5932が1件あり、未解消のモジュール指摘として記録する。openpgpはimportしていない。依存更新時・リリース時に再検査する。
