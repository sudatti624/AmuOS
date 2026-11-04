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

## バックアップ形式の設計調査（順序0）

順序4の実装候補をage v1形式、filippo.io/age v1.3.2、X25519 recipientに固定する。一次資料は[形式仕様](https://age-encryption.org/v1)、[v1.3.2](https://github.com/FiloSottile/age/releases/tag/v1.3.2)、[ライセンス](https://github.com/FiloSottile/age/blob/v1.3.2/LICENSE)。ライセンスはBSD-3-Clause。分割されたペイロードの認証と最終チャンク検証を利用し、独自のストリーム暗号方式を実装しない。まだgo.modへ追加・バイナリへ同梱していない。導入時には同版と推移依存の脆弱性・ライセンスを再確認し、配布時の表記を同梱する。
