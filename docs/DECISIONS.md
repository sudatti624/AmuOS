# 設計判断

## ADR-001: 最初の実装単位

仕様v0.4 §16の順序に従い、認証・認可・監査を先に実装する。AgentとUIは未実装として扱い、ダミーのホスト監視値は提供しない。Phase 0の全体ゲートは設計資料を用意しただけで完了扱いにしない。

## ADR-002: 暗号と通信

Go 1.27系、chi v5、modernc.org/sqliteを採用。パスワードはgolang.org/x/crypto/argon2のArgon2id（メモリ64MiB、反復3、並列度2、ランダム16バイトsalt、32バイト出力）。セッションとCSRFはそれぞれ独立した32バイト乱数。DBにはSHA-256のみ保存する。比較は定数時間。パスワードは12〜128バイト、リクエスト本文は16KiB上限。生の秘密値を監査しない。

HTTPSを必須とする。テストのhttptest経路を除きHTTPで起動しない。X-Forwarded-Forや転送先Originを信頼せず、外部Originは明示設定する。ログイン失敗制限は接続元IPで評価し、メモリの上限を設ける。

## ADR-003: 初期対応環境

Linux amd64、Ubuntu Server 24.04 LTSおよびDebian 13を初期検証対象とする。Windowsは開発・単体テスト専用。配布前に両Linuxのクリーン環境で導入・停止・更新・復元を実証する必要があり、現時点では対応済みと宣言しない。

## ADR-004: ツールと未完了条件

Goは公式アーカイブのSHA-256検証後にローカル使用。正確な依存版はgo.mod/go.sumに固定する。sqlc/gooseのCI生成・移行、React/TypeScript・Vite+・Tailwind・shadcn/uiの画面基盤は後続タスク。Vite+の正式セットアップ前に別のビルド基盤を確定しない。

## ADR-005: OSSライセンスと公開形態

Apache License 2.0で https://github.com/sudatti624/AmuOS に公開する。LICENSEは正規全文を無改変で置き、依存（MIT/BSD-3-Clause）にNOTICEを要するものがないためNOTICEは作らない。Goモジュール名は公開URLに合わせる。脆弱性はGitHubの非公開報告で受け付ける。貢献はApache-2.0第5条に従い、CLA・DCOは導入しない。開発ルールはCONTRIBUTING.mdに集約する。
