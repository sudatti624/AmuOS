# 基盤の設計契約

仕様v0.4 §2・§10・§12・§14・§16を具体化する。2026-11-04時点の順序0の設計基準。実装済みAPIはAPI.mdとAPI_AUTHORIZATION.csv、後続の未提供機能は本書の契約で区別する。順序3の画面には着手しない。

## 対応環境・構成・版

- 配布対象はLinux amd64、Ubuntu Server 24.04 LTS / Debian 13。Windows amd64は開発・テスト用。単一Control Planeを前提とし、複数レプリカ・共有ネットワークファイルシステム上のSQLiteは対象外。
- Go 1.27系（検証1.27.3）、chi v5.5.0、modernc.org/sqlite v1.61.1、x/crypto v0.58.0。正確な推移依存はgo.mod/go.sumを基準とする。DBはWAL・外部キー有効・busy_timeout 5秒・単一接続。未適用移行全体を一つのトランザクションで適用する。
- Frontendは仕様のReact / TypeScript / Vite+ / Tailwind v4 / shadcn/uiを維持する。依存導入とlockfile・ビルド・画面/E2Eは順序3。今回の設計完了をFrontend導入済みとは扱わない。
- sqlcでの生成・goose実行基盤の統合は、既存の原子的移行・クエリ契約を維持して順序4の配布/移行整備で検証する。現時点の実行方式は組込みSQLとuser_versionで確定しており、未導入ツールを実行前提にしない。
- Linux配布はtar.gz、実行ファイル `/usr/local/bin/amuos-controlplane`・`amuos-agent`、設定 `/etc/amuos`、状態 `/var/lib/amuos`、ロック `/run/amuos`。状態ディレクトリ0700、鍵/DB0600、専用実行ユーザー。導入・systemdと手動更新の実装/OS実証は順序4。
- シークレット保存はAES-256-GCM、32バイトのデータ鍵と値ごとの乱数nonceを使用する。鍵IDと形式版を保存し、鍵ファイルはDBと分離する。現在のパスワードはArgon2id、トークン類はSHA-256であり、復号可能な形で保存しない。

## 信頼境界・権限

ブラウザ→HTTPS API、Control Plane→SQLite/鍵ファイル、ホスト→Agent専用リスナー、OS管理者→ローカル復旧を独立した境界とする。Origin・転送ヘッダー・要求者の自己申告IDを権限として信用しない。

- 実装済み全27ルートの認証・操作・範囲・CSRF・再認証はAPI_AUTHORIZATION.csvに固定する。自動試験がchiの全ルートと照合し、全sessionルートの未認証拒否と変更要求のCSRF欠落拒否を検証する。
- Ownerはユーザー直接・全体のみ。Adminは自己の操作/範囲の部分集合だけを付与できる。OwnerとOwnerを含むグループの変更は再認証済みOwnerに限定する。
- 自動保持と冪等記録の期限処理は固定主体system。設定に従った期限処理だけを行い、権限や資格情報を新規付与しない。ブラウザからsystemを指定する入力・エンドポイントは設けない。
- 後続の配信は購読開始だけでなくイベント配送時も最新の有効性・範囲・authorization_versionを照合する。失効時は配信を終了し、古い接続の再利用を許可しない。

## 現在の永続化と原子性

users、groups、memberships、grants、sessions、bootstrap、invitations、invitation_grants、hosts、audit_events、audit_settings、idempotency_recordsの具体形は移行SQLを基準とする。

| 操作 | 同時に確定するもの | 失敗/再開の規則 |
|---|---|---|
| 初回設定/招待受諾 | 一回限り消費・ユーザー・固定割当・監査 | 全体ロールバック。一件だけ成功 |
| 権限/所属/有効性 | 更新・認可版・招待取消・再認証クリア・監査 | 次の要求から反映。監査失敗なら変更なし |
| Owner移譲 | 両者の固定割当・版・招待取消・全セッション失効・監査 | 最後のOwnerを失わず、部分確定しない |
| ホスト登録枠 | ホスト・要求者/操作/範囲付き冪等記録・結果参照・監査 | 同一キー/入力は再取得。異入力・期限切れは409 |
| 自動保持 | 予定取得・最大1000件削除・実施監査・次回予定 | 未コミットならすべて戻る。失敗は5分後に再試行 |

冪等キーは結果保持30日後もハッシュの拒否記録を残す。入力・結果参照は自動処理で除去する。HTTPのrequest_idは試行ごとに新しく、業務の同一性を示すキーと混同しない。応答喪失だけでは失敗/未実行と判断しない。

HTTPではホスト作成のキーを必須にする。内部Storeのキーなし呼出しはテスト準備などのローカル処理用で、HTTPからは到達させない。公開APIの保証と内部の単発呼出しを混同しない。

## 後続: Agentプロトコル（順序5、未実装）

通信はHTTPS JSON・TLS 1.3。通常のブラウザ/登録APIは8443、登録後のAgentは専用9443でmTLS必須。接続先名と信頼アンカーを導入時に安全な経路で配布する。検証無効化・初回接続先の無条件受容は行わない。

初期wire版はprotocol_version=1 / schema_version=1。Control Planeの次の製品版も直前のAgentが使うv1経路を維持し、変更は能力交渉済みの操作だけに限定する。未知の操作/必須形式版は409とし、TLS検証を緩めるフォールバックはしない。応答の追加任意フィールドは旧Agentが無視できる形式とする。

| API | 主体・入力 | 認可・確定条件 |
|---|---|---|
| POST /api/v1/hosts/{id}/enrollment | session、CSRF、TTL 1〜30分（既定10分） | host.manage(id)。pending対象、再発行で旧トークン失効。新規ID確保は全体管理権限 |
| POST /api/v1/agent/enroll | 登録要求ID・一回限りトークン・署名検証可能なCSR | ホスト固定。鍵所持証明、消費・Agent/鍵/証明書・結果の原子的保存 |
| POST /api/v1/agent/enrollments/{id}/challenge | 登録要求ID | 30秒・一回限りの乱数チャレンジ。資格情報を返さない |
| POST /api/v1/agent/enrollments/{id}/recover | チャレンジID・登録鍵による署名 | 同じ公開鍵の登録結果のみ再取得。失効/削除は拒否 |
| POST /agent/v1/credentials/renew | 有効なmTLS資格情報・新CSR | 現在の登録と鍵を再検証、新鍵の所持証明 |
| POST /agent/v1/heartbeat | mTLS、Agent版・対応能力 | 証明書シリアルとAgent/ホストの現在状態を照合 |
| POST /agent/v1/samples | mTLS、run_id・連番・収集時刻・サンプル | 自ホストのみ。同じキーで異なる内容は拒否。失効照合と受入を同一トランザクションで行う |
| POST /agent/v1/tasks/poll | mTLS、最終受領結果 | 自ホストの許可済みタスクだけを配送 |
| POST /agent/v1/tasks/{id}/result | mTLS、試行ID・入力ハッシュ・結果 | タスク/ホスト/試行一致。再送は同じ結果として受領 |
| DELETE /api/v1/hosts/{id} | session・CSRF | host.manage(id)。接続遮断、トークン/全証明書失効と削除記録・監査を原子的に保存 |

Agent IDとホストIDはサーバー発行の不変ID。鍵はAgent側でECDSA P-256を生成し、秘密鍵を送信しない。証明書はclientAuth用途・Agent IDのURI SAN・一意シリアルで発行する。SAN形式は `amuos://agent/<agent_id>`、実際のホスト所属はDBの対応を使用し、Agentから指定させない。

Agent/証明書/登録要求/チャレンジの設計データは以下に固定する。

| データ | 必須項目・一意性 |
|---|---|
| agents | agent_id PK、host_id FK、public_key_hash、pending/active/revoked/deleted、last_seen、version |
| agent_credentials | serial PK、agent_id FK、public_key_hash、cert_pem、issued_at/not_after、revoked_at、旧鍵移行期限 |
| enrollments | request_id PK、host_id FK、token_hash UNIQUE、issuer_id、expires_at、consumed_at/revoked_at、固定公開鍵と結果参照 |
| challenges | challenge_id PK、enrollment_id FK、nonce_hash、expires_at、consumed_at |
| samples | agent_id/run_id/sequence UNIQUE、payload_hash、collected_at/received_at、対象メトリクス |

資格情報30日、残り10日から更新。新資格情報の利用確認後に旧資格情報を失効し、猶予は最大24時間。期限切れ・侵害時は安全な新規登録に戻す。署名鍵とサーバーTLS鍵・Agent秘密鍵を分離する。各受入と配送で現在状態を確認し、CA署名だけで継続許可しない。

登録本文16KiB、認証済みAgent本文256KiB、送信は1回最大40サンプル。定常収集15秒、欠測バッファ10分/10MiB、超過は古いものから破棄し欠測記録を残す。run_idはAgentプロセス起動ごとの乱数で、未送信データは元のrun_id/連番を保持する。OS再起動識別子は差分メトリクス用に別フィールドとする。未来120秒超・保持期間外・型/範囲/サイズ不正は拒否する。

Agent要求はAgent単位120回/分、未登録/回復はIP単位5回/15分。TLS未認証はHTTP処理へ到達させない。再接続は1〜60秒の指数バックオフとジッター。接続を失敗扱いにする前に既存結果を照合する。

## 後続: タスク・障害回復（順序4・5、未実装）

タスクはtask_id、requester_id、operation、target_id、input_hash、idempotency_key_hash、state、attempt_id、受付/実行期限、結果/理由、関連request_id、時刻を保存する。要求者・操作・対象・冪等キーを一意とする。試行とAgentの受付/結果も永続化し、結果受領確認前は消去しない。

| 事象 | 状態・処理 |
|---|---|
| 受付 | queued。入力・現在の認可・期限を検証し受付イベントを保存 |
| 配送 | dispatchingを保存してから配送。受付確認でrunning |
| 確認済み結果 | succeeded/failed。対応する試行と結果を保存 |
| 配送前の期限/取消 | 未実行を確認してtimed_out/cancelled |
| 配送後の応答喪失/停止未確認 | unknown。成功/失敗と推測しない |
| 再接続/再起動 | 同じタスク/試行の受付・結果を照会。未確定の変更操作を自動再実行しない |
| 権限/Agent失効 | 配送前に再検証して拒否。実行済みの副作用の取消は保証しない |

読み取りは受付60秒/実行30秒、バックアップは受付60秒/実行15分。読み取りの新試行は結果照合後に最大2回。変更操作のunknownは手動照合・根拠の監査後に別タスクで扱う。終端と重複排除は30日、unknownは解決まで保持する。期限切れの配送は記録の有無にかかわらず拒否する。

APIはsession・CSRFで `POST /api/v1/hosts/{id}/tasks`（collectのみ、host.collect）、`GET /api/v1/tasks` / `GET /api/v1/tasks/{id}`（親ホストのhost.readで絞込）、`POST /api/v1/tasks/{id}/cancel`（host.collect）、`POST /api/v1/tasks/{id}/resolve`（host.manage、根拠必須）を採用する。任意コマンド入力は受け付けない。

## 後続: バックアップ形式・復元（順序4、未実装）

独自のストリーム暗号形式を作らず、age v1形式・X25519 recipientを採用する。実装予定はfilippo.io/age v1.3.2（BSD-3-Clause）。依存導入と脆弱性再確認・ライセンス同梱は順序4。現在のgo.modへ未使用依存を追加しない。

暗号化ストリームの内容はtarで、許可する通常ファイルだけを含める。manifest.json（最大64KiB）、database.sqlite、config.json、keys.jsonを必須とする。manifestには形式版1、製品版、スキーマ版、設定版、バックアップID、UTC一貫性境界、各ファイルのサイズ・SHA-256、鍵ID・対象一覧を含める。復旧用秘密鍵は含めず、別の障害領域に保管する。大きなDBを全量メモリへ読み込まない。

初期の展開上限はDB10GiB、設定/鍵各1MiB、全体12GiB。上限超過は明示的な失敗とし、切り詰めない。メモリ使用量はストリームのバッファと小さなmanifestに制限する。

初期の手動コマンドは `--backup-out <archive.age> --recipient <public-key>`、復元は `--restore-from <archive.age> --recovery-key-file <private-key>` とし、通常サーバー起動・初回設定と排他的なモードにする。OS管理権限と保護された状態ファイルへのアクセスを必要とする。オンライン復元APIは提供しない。

1. Control Planeを停止し、同じ状態領域の排他ロックを取得する。Linuxはflock、Windows開発実装はLockFileExを使用し、二重起動・バックアップ/復元の競合を拒否する。
2. SQLiteバックアップAPIで一貫したDBを取得し、同じ境界の設定・鍵束・manifestを暗号化ストリームへ書き出す。平文DBの単純コピー、秘密鍵のログ/引数値への出力は禁止する。
3. 完全性とDB整合性を検証して完成名で原子的に公開する。容量不足・中断・暗号化失敗では成功成果物にしない。
4. 復元はネットワーク隔離・空の0700一時ディレクトリで実施する。同一製品版のみを初期保証範囲とし、暗号認証・最終チャンク・manifest・必須ファイル・サイズ/ハッシュ・SQLite整合性・鍵による復号を確認する。
5. 絶対パス、`..`、リンク、重複名、未許可エントリ、宣言サイズ超過を拒否する。全部検証するまで現行の状態一式を置換しない。退避状態を残し、失敗時は停止中に一式で戻す。
6. 全セッション・未使用招待/登録トークンを無効化し、Agent CAを再発行して旧CAを信頼対象から除く。復元済みデータ鍵で既存値を復号後、新鍵へ再暗号化する。
7. queuedを含む未完了タスクはunknownで保留。Ownerがユーザー・権限・外部の実状態を照合して再開する。旧Agentは安全な経路で新規登録する。

初期リリース検証は誤鍵、切詰め、改ざん、必須ファイル欠落、非互換版、容量不足、各切替段階の中断、再登録前の旧資格情報拒否を含める。手順・性能・独立環境復元の実証は順序4/6で行い、設計レビューを実証済みとは扱わない。

## 後続操作の認可・配信・エラー

| 操作 | 必要権限/範囲 | 再認証 |
|---|---|---|
| ホスト一覧/詳細/履歴/systemd状態 | host.read、ホストIDで絞込 | 不要 |
| 登録トークン/更新取消/ホスト失効 | host.manage、ホストID | 不要。全体範囲だけがIDを新規確保 |
| メトリクス/タスクの配信購読 | 認証済みユーザーの対象ホスト範囲 | 各配送で版を照合 |
| ユーザー/グループ/監査の配信 | 対応する全体identity.manage / audit.read | 各配送で版を照合 |
| 保持設定/Owner変更/バックアップ取得 | 対応するOwner操作 | 5分以内 |
| ローカル復元/Owner回復/CA再発行 | OS管理権限・復旧鍵、公開APIなし | ローカル復旧手順で代替 |

後続配信は `GET /api/v1/events` のSSEとし、イベントIDで再開できても保存済み権限を引き継がない。未認証401、範囲外リソース404、認可拒否403、キー/状態競合409、サイズ/形式不正400/413、監査/容量障害503、互換性不一致409を採用する。Agent資格情報が無効ならTLSまたは401で拒否する。

ファイル管理、Docker、デプロイ、VM、systemd変更のAPI・Adapterは初期リリースに登録しない。Control Plane/Agentの停止・更新は管理対象ワークロードへ停止命令を送らない。
