# OSS調査

最初の実装対象は認証・RBAC・監査。参照先は上流の一次資料に限定する。

| 対象 | 参照・判断 |
|---|---|
| Go | https://go.dev/dl/ — 公式配布・SHA-256検証、Go 1.27系 |
| chi | https://pkg.go.dev/github.com/go-chi/chi/v5 — net/http互換ルーティング、MIT |
| modernc SQLite | https://pkg.go.dev/modernc.org/sqlite — CGoを不要にしWindowsテストとLinuxビルドを両立、BSD-3-Clause |
| Argon2 | https://pkg.go.dev/golang.org/x/crypto/argon2 — Argon2idの実装を利用、BSD-3-Clause |
| SQLite | https://sqlite.org/backup.html — 稼働中ファイルの単純コピーを避けるバックアップ契約 |

実際の採用版はgo.mod/go.sum。全依存のライセンスはMIT/BSD-3-ClauseでApache-2.0と両立する。バージョン更新時の既知脆弱性確認と、バイナリ配布時のライセンス同梱は未完了。Docker・ファイル管理・仮想化のOSS調査は各機能の着手前に行う。
