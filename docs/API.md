# API契約

## 監視設定

`GET /api/v1/monitoring/settings` と `PUT /api/v1/monitoring/settings` は全体範囲のOwner/Adminに限定する。変更はOrigin・CSRFを必須とし、変更前後の設定と監査を同じDBトランザクションで確定する。再認証は不要。

本文は `sample_interval_seconds`（5〜300）、`raw_retention_days`（1〜30）、`rollup_retention_days`（7〜400）の整数3項目を必須とする。集約保持期間は生データ以上とする。欠落・null・未知フィールド・不正な値を拒否する。収集間隔は次のAgentハートビートで通知し、保持設定は次の定期処理から適用する。保持期間を延ばしても削除済みデータは回復しない。

基準: 仕様v0.4 §10、§12。HTTPS JSON、ルート `/api/v1`。秘密値をクエリパラメーターに含めない。

| メソッド・パス | 入力 | 認可・結果 |
|---|---|---|
| GET /healthz | なし | 公開、生存確認のみ。DBや依存先のreadinessを保証しない |
| POST /setup | token, username, password | ローカル発行の10分・一回限り資格情報。201 |
| POST /auth/login | username, password | 有効ユーザーとパスワード。200とCookie、csrf_token |
| POST /auth/logout | 本文不要、X-CSRF-Token | 有効セッション、Origin・CSRF一致。200、セッション削除 |
| GET /me | Cookie | 有効セッション。id・本人のusername・最新の範囲付き割当・authorization_version |
| GET /hosts | Cookie | 最新のhost.read範囲のみ。空の場合はitems: []。削除済みは非表示。登録済みならagent（版、最終受信、接続中か、資格情報の期限）を含む |
| POST /hosts | Cookie、name、X-CSRF-Token | 全体host.manageのみ。pending IDを確保、201。Agent登録・実測値ではない |
| GET /audit | Cookie | audit.read範囲のみ、最新100件まで。全体イベントには全体権限が必要 |

変更要求・ログイン・初期設定に設定値と完全一致のOriginヘッダーを必須とする。CSRFトークンはログイン応答時にのみ返す。紛失時は再ログインする。Cookieは `__Host-amuos_session`、Secure/HttpOnly/SameSite=Strict、Path=/、Domainなし。アイドル30分・絶対12時間。OriginのないCLIも設定値を明示して送信する必要がある。

パスワード12〜128バイト。usernameは小文字化した3〜64文字の英数字・`_-.`。本文16KiB、未知フィールド・複数JSON・不正JSONを拒否する。認証系は送信元あたり15分に5回まで（成功・不正入力を含む）、X-Forwarded-Forは使わない。IPv6は/64を1つの送信元として数える。送信元の表は最大65536件で、満杯のときは新しい送信元が共有の枠（15分に64回）を使い、一律には拒否しない。同時パスワード計算は1件までに制限する。

| HTTP | エラー |
|---|---|
| 400 | invalid_input / invalid_json |
| 401 | unauthorized（未認証・無効・期限切れを区別しない） |
| 403 | forbidden / origin_rejected |
| 404 | not_found（アカウント・招待管理の対象不存在） |
| 405 | method_not_allowed |
| 409 | conflict（初期化済み、既存ユーザー名、使用済み招待、最後の有効Ownerの無効化など） |
| 415 | json_required |
| 429 | rate_limited、Retry-After付き |
| 500 | internal_error（DB・監査等の詳細を外部へ公開しない） |
| 503 | auth_busy / audit_unavailable、Retry-After付き |

セッション認証と最新割当の取得・状態更新・監査は同じトランザクション。Origin/JSON形式の不正は業務処理前に拒否し、拒否イベントを監査する。バックアップAPIは未公開。タスクとAgentは下の各節を参照。

## ホスト登録枠の冪等性

`POST /hosts` はIdempotency-Keyを必須とする。16〜128文字の英数字・ハイフン・アンダースコア・ピリオドで、操作ごとに十分な乱数を使う。欠落/形式不正は400 idempotency_key_required。通信断の再要求では同じキーと同じnameを送る。

キーは要求者・host.create・ホスト台帳の範囲で一意。同じキー・正規化済みnameには最初の201応答のホストIDを返し、Idempotency-Replayed: trueを付ける。nameは前後の空白だけを除去する。別入力での再利用、期限切れ、削除/失効済みリソースへの再要求は409。再要求でも現在の認証・認可・CSRFを検証し、以前の権限だけで結果を返さない。

ホスト作成・入力/キーのハッシュ・結果参照・監査は一つのSQLiteトランザクションで保存する。応答が失われても、再起動後の再要求で別ホストを作らない。結果参照は30日保持する。その後は自動処理で参照と入力ハッシュを除去し、キーのハッシュと要求者・操作・範囲の拒否記録を保持するため、古いキーを再利用して新規作成することはできない。リクエストIDは各要求で新しく、冪等キーとは別の識別子である。

この契約は順序1のホスト登録枠作成に適用する。招待トークンは一回限り、グループ名は一意、PUTは各APIの状態置換として扱う。Agent/外部実行のタスクは順序4・5で別の永続状態機械を実装する。結果不明の変更操作に新しいキーを自動発行して再実行しない。

## タスク

管理対象ホストへの読み取り専用の作業（現在は `collect` のみ）を永続化する。任意コマンドの入力は受け付けない。Agentは順序5で実装するため、現時点では配送されず、受付期限の60秒を過ぎたタスクは自動処理で `timed_out`（reason: not_dispatched）になる。

| メソッド・パス | 入力 | 認可・結果 |
|---|---|---|
| POST /hosts/{id}/tasks | Cookie、X-CSRF-Token、Idempotency-Key、`{"operation":"collect"}` | 対象ホストのhost.collect（Admin・Operator）。201、state: queued。同じ要求者・ホスト・キーの再要求は元のタスクとIdempotency-Replayed: true。1ホストの未完了タスクは10件まで（超過は409） |
| GET /tasks | Cookie | host.readのホストのタスクだけ、新しい順に100件まで |
| GET /tasks/{id} | Cookie | 親ホストのhost.read。範囲外・不存在は404 |
| POST /tasks/{id}/cancel | Cookie、X-CSRF-Token、`{}` | 親ホストのhost.collect。queuedのみcancelledにする。配送後は実行の有無を確認できないため409 |
| POST /tasks/{id}/resolve | Cookie、X-CSRF-Token、`{"resolution":"failed","evidence":"…"}`（resolutionはsucceededかfailed、evidenceは1〜500バイト） | 親ホストのhost.manage（Admin）。unknownのみ。根拠を監査に記録し、再実行はしない |

状態は queued・dispatching・running・succeeded・failed・timed_out・cancelled・unknown。範囲外のホスト・タスクは存在しないものと同じ404を返す。作成・取消・解決と監査は同じトランザクションで確定する。

自動処理（毎分）は、受付期限切れのqueuedをtimed_out、実行期限切れのdispatching・runningをunknownにし、終了から30日たったタスクを削除する。unknownは人が解決するまで残す。起動時には前回の停止で結果が分からなくなったdispatching・runningをunknownにする。復元したDBでは、queuedを含む未完了のタスクをすべてunknownにする。終了したタスクを削除したあとは、同じキーで新しいタスクを作れる（読み取り専用のため重複しても副作用はない）。

## Agentの登録と信頼

Agentは登録だけをブラウザ用のポート（既定8443）で行い、登録後はmTLS専用のポート（既定9443、`--agent-addr`）だけを使う。mTLSのポートは、Agent CAが発行したクライアント証明書がなければTLSの段階で接続を拒否し、HTTPの処理に到達させない。各要求では、証明書の署名に加えて、DBの資格情報・Agent・ホストの状態を同じトランザクションで照合する。

| メソッド・パス | 入力 | 認可・結果 |
|---|---|---|
| POST /hosts/{id}/enrollment | Cookie、X-CSRF-Token、`{"ttl_minutes":10}`（1〜30、省略時10） | 対象ホストのhost.manage。pendingのホストだけ。201で一回限りの `code` を一度だけ返す。再発行すると未使用の旧コードは無効 |
| POST /agent/enroll | `{"code":"…","csr":"base64のPKCS#10"}` | コードの対象ホストに新しいAgent IDを割り当て、30日のクライアント証明書を返す。鍵はECDSA P-256、CSRの署名で所持を確認し、CSRの名前などは使わない。使用済みは409、期限切れ・失効・不一致は401 |
| POST /agent/enrollments/{id}/challenge | `{}` | 使用済みの登録に対する30秒・一回限りのチャレンジ |
| POST /agent/enrollments/{id}/recover | `{"challenge_id","nonce","signature"}` | 登録時の鍵で署名した場合だけ、元の登録結果を返す。失効・削除・更新済みは401 |
| POST /hosts/{id}/revoke | Cookie、X-CSRF-Token | host.manage。Agentの全資格情報と未使用コードを失効し、queuedのタスクを取り消す。ホストと履歴は残る |
| DELETE /hosts/{id} | Cookie、X-CSRF-Token | host.manage。失効と同じ処理のうえ、ホストを一覧から外す。監査ログは残る |
| POST /agent/v1/heartbeat（mTLS） | `{"version","capabilities":[]}` | 自身の状態を更新。`renew_after`（更新を始める時刻）と `sample_interval`（収集間隔の秒数）を返す |
| POST /agent/v1/credentials/renew（mTLS） | `{"csr"}`（新しい鍵） | 後継の資格情報を発行。同じ要求の再送には同じ後継を返す。旧資格情報は後継が使われた時点、遅くとも24時間後に失効 |

登録系の3つは、ログインと同じ送信元あたり15分に5回の制限を共有する。mTLSの要求はAgentあたり1分に120回まで。最終受信から60秒を過ぎたAgentは `connected: false` と表示する。復元したDBではすべてのAgentの資格情報を失効し、ホストをpendingに戻し、Agent CAを作り直すため、各Agentは新しいコードで登録し直す。

## 監視値とホスト情報

Agentは収集間隔（既定15秒）ごとにLinuxの `/proc` などから監視値を読み、mTLSで送る。ホスト情報は起動時と10分ごとに送る。どちらも、所属ホストは資格情報から決め、要求の内容では指定させない。取得できなかった値は `null` とし、0にしない。

| メソッド・パス | 入力 | 認可・結果 |
|---|---|---|
| POST /agent/v1/samples（mTLS） | `{"run_id","samples":[{"seq","collected_at","cpu",…}]}`（1〜120件、64KiBまで） | `(Agent ID, run_id, seq)` で重複を除き、`{"accepted","duplicates","expired"}` を返す。10分（転送の1分を含めて11分）より古いもの、または集約済みの区間に入るものは `expired` として保存しない。1分を超える未来の時刻、範囲外の値（使用率は0〜100、そのほかは0以上）、不明な項目を含むバッチは全体を400で拒否する |
| POST /agent/v1/host-info（mTLS） | ホスト名・OS・カーネル・CPU・メモリ・ファイルシステム（64件まで）・インターフェース（64件まで）・起動時刻と `collected_at` | 204。受け取ったものより古い取得時刻の情報では上書きしない。制御文字や256バイトを超える文字列は400 |
| GET /hosts/{id} | Cookie | 対象ホストのhost.read。範囲外・削除済みは404。ホスト、Agent、`info`（取得時刻と受信時刻つき）、`latest`（最新の監視値。受信から60秒を過ぎると `current: false`）、`sample_interval` を返す |
| GET /hosts/{id}/metrics?range= | Cookie、`range` は1h・6h・24h・7d・30d（省略時1h） | 対象ホストのhost.read。各ステップ（15秒・1分・5分・30分・2時間）の平均・最小・最大を `times` と `series` で返す。データのないステップは `times` に含めないため、画面では欠測として表示し、0で補わない |

監視値の項目は、CPU使用率、負荷（1・5・15分）、メモリ・Swap・ディスク（/）の使用率、ディスクI/Oとネットワークの毎秒バイト数、稼働時間。I/Oは累積値の差分から計算し、最初の値やカウンターの巻き戻り（再起動など）では `null` にする。ネットワークは仮想インターフェースを除き、ディスクはパーティションやdevice-mapperを除いて二重に数えない。

生データは7日、5分単位の集約（件数・最小・最大・平均）は30日保持する。毎分の自動処理が、受け付けうる最も遅い値より前の5分区間を集約し、集約済みかつ保持期間を過ぎた生データだけを削除する。保持期間と収集間隔の設定値はDBにあり、バックアップに含まれる。画面やAPIから変更する手段はまだない。

## リクエストIDと拒否の監査

すべての応答にサーバー生成の `X-Request-ID` を返す。クライアントから同名ヘッダーが送られても採用しない。対応する監査イベントには同じrequest_idを保存する。ローカル操作や移行前のイベントにはrequest_idがない場合がある。

Origin・JSON・入力検証・CSRF・認証・認可・レート制限・認証処理の混雑、および404/405の拒否を `http.reject` として記録する。業務層が既に記録した認証失敗や認可拒否とは別のイベントで、同じrequest_idで関連付く。対象はHTTPメソッドと登録済みルートのパターンに限り、未一致はunmatchedとする。本文、クエリ文字列、実際のURL内ID、Cookie、トークン、Originの値、クライアントのリクエストIDは保存しない。

拒否時の主体は、有効なセッションが確認できた場合のみ本人とし、それ以外はanonymous。拒否ログのためにセッションの最終利用時刻を更新しない。切断によるキャンセル後も最大2秒で監査を試み、保存できない場合は503 audit_unavailableを返す。個別の記録は送信元ごとに1分60件、全体で1分600件までとし、超えた拒否も通常どおり応答したうえで件数だけを数え、次の時間枠の最初の拒否時に `http.reject.suppressed`（details.suppressed_count・window_seconds）としてまとめて記録する。これにより、認証なしの大量要求で監査ログやDB接続を占有できないようにする。保存障害は秘密値を含まないリクエストID付きログで通知する。正常な生存確認は監査保存障害だけで停止しない。

## 監査ログの保持

| メソッド・パス | 本文・認可・結果 |
|---|---|
| GET /audit/retention | 全体audit.read。保持期間、自動実行の有効性、次回/最終実行、失敗件数・固定エラーコードを返す、200 |
| PUT /audit/retention | retention_days（必須整数、1〜3650日）、automatic_enabled（任意bool）。再認証済みOwner・Origin・CSRF。200 |
| POST /audit/retention/run | 本文は空のJSONオブジェクト `{}`。再認証済みOwner・Origin・CSRF。期限切れ削除を最大1000件実行し、結果を200で返す |

既定保持期間は180日。削除対象は `at < 現在UTC時刻 - retention_days × 24時間` のイベントのみで、境界ちょうどのイベントは保持する。通常APIに任意の監査イベントの変更・個別削除機能は設けない。runの本文でIDや別の期限を指定することもできない。

実行結果はretention_days、cutoff（Unix秒）、deleted_count、first_deleted_id、last_deleted_id、more_expiredを含む。期限切れが残る場合のみmore_expiredがtrueとなる。

自動実行は既定で有効。サーバーは起動後と毎分に実行予定を確認し、通常は1時間ごと、残件がある場合は1分後に最大1000件を処理する。Ownerがautomatic_enabledをfalseにすると停止する。保持日数や有効性を変更すると次の確認時に新しい設定を適用する。更新要求自体の中では削除しない。日次境界ではなくUTCの24時間単位で判定する。

実行予定の取得、削除、監査、次回予定は同じDBトランザクションで確定する。再起動でも予定を保持し、同じ予定を二重実行しない。自動処理の主体はsystem。監査保存失敗では削除を戻し、固定エラーコードと失敗件数を状態に記録して5分後に再試行する。サーバー停止では処理をキャンセルし、DBを閉じる前にワーカーの終了を待つ。

監査イベントにはdetailsオブジェクトを追加する。設定変更のbefore_days/after_days、削除の基準・件数・ID範囲・残件有無を記録し、設定更新/削除とそのイベントを同じトランザクションで確定する。監査保存に失敗した場合は削除も戻し、成功を返さない。既存イベントのdetailsは空オブジェクトとして移行する。

アカウント・グループ・割当・Owner操作の成功にはdetails.before/afterを保存する。対象ID・名前・有効性・認可バージョン・直接割当・所属・セッション件数・未使用招待件数など、操作に必要な項目だけを抽出する。Owner移譲は移譲元・先の両方を含む。パスワード、ハッシュ、セッショントークン、CSRF、招待トークンは含めない。拒否時には対象の状態を記録しない。詳細生成・保存が失敗した場合も業務更新を戻す。詳細の上限は64KiB。

## アカウント管理の契約

仕様§10.3〜10.5に従う追加API。実装と検証の到達状況はPROGRESS.mdに記録する。

| メソッド・パス | 入力 | 認可・結果 |
|---|---|---|
| POST /auth/reauthenticate | password | 有効セッション・CSRF・現在のパスワード。成功時にセッションへ再認証時刻を記録、200 |
| POST /auth/password | current_password, new_password | 本人の現在パスワード・有効セッション・CSRF。変更後は本人の全セッションを失効し、200 |
| GET /users | Cookie | 全体identity.manage。id・username・enabledのみ、200 |
| PATCH /users/{id}/enabled | enabled（必須bool） | 非Owner対象は全体identity.manage。Owner対象はOwner専用かつ直近5分以内の再認証。最後の有効Ownerの無効化は409 |
| POST /invitations | username, grants | 全体identity.manage・CSRF。非Ownerの範囲付き固定ロールのみ。id・token・username・expires_atを201で一度返す |
| POST /invitations/accept | token, password | セッション不要、Origin一致・認証入口の回数制限。招待で指定された名前と割当でアカウントを作成、201。自動ログインしない |
| DELETE /invitations/{id} | CSRF | 全体identity.manage。取消のみ、204。不存在は404、使用済みは409 |

すべての変更要求で設定値に一致するOriginを要求する。招待受諾以外は有効セッションとCSRFが必要。パスワード検証を伴う経路は認証入口の回数・並列数制限も適用する。

招待は32バイト乱数、有効期限24時間、一回限り。DBにはハッシュだけを保存する。usernameは発行時に固定し、既存ユーザーへの発行は409。同じ名前へ再発行すると以前の未使用招待を取り消す。最大32個の割当を保存し、範囲はall_hostsまたは単一host_idで表現する。Owner招待は許可しない。発行者の権限の部分集合であることを発行時と受諾時に確認し、受諾者が名前・ロール・範囲を変更する入力は受け付けない。

招待発行の本文例（host_idは既存のホストIDに置き換える）：

```json
{
  "username": "member",
  "grants": [{"role": "Viewer", "all_hosts": false, "host_id": "<host-id>"}]
}
```

全体範囲は `"all_hosts": true` としてhost_idを省略する。これは将来追加されるホストにも適用される。招待受諾の本文はtoken・passwordのみ。トークンはURLに含めず、安全な経路で本人へ渡す。

受諾時に期限切れ・使用済み・取消済み・発行者無効・発行者の権限不足・対象ホスト削除が判明した招待は拒否する。無効化された発行者の未使用招待も取り消す。無効トークンの理由やユーザー名は応答に含めず401を返す。正常な招待とユーザー・割当・監査の保存は一つのトランザクションで行う。

再認証は通常ログインと別の操作とし、他のセッションには引き継がない。再認証から5分以上経過・時刻逆行は403。非OwnerがOwner状態を変更しようとした場合は403、対象IDの不存在は認証・全体権限確認後に404。無効化時は対象の全セッションを削除し、再有効化しても復活させない。

## グループと固定ロールの管理

以下はすべて全体identity.manageを必要とする。変更にはOriginとCSRFも必要。Ownerを含むグループの変更やOwnerの所属変更はOwner専用とし、同じセッションで5分以内の再認証を要求する。

| メソッド・パス | 本文・結果 |
|---|---|
| GET /groups | itemsにid・name・user_ids・grantsを返す、200 |
| POST /groups | name。ユーザー名と同じ3〜64文字の形式で小文字化。空グループを201で作成、名前重複は409 |
| PUT /groups/{id}/members | user_ids配列で所属全体を置換、200。最大256人。空配列で解除。欠落/null・重複IDは400 |
| PUT /groups/{id}/grants | grants配列でグループ割当を置換、200。最大32個。空配列で解除。欠落/null・重複割当は400 |
| DELETE /groups/{id} | 所属と割当を同じトランザクションで削除、204 |
| GET /users/{id}/grants | itemsに直接割当のみを返す、200。グループ経由の実効権限とは区別する |
| PUT /users/{id}/grants | grants配列で非Ownerの直接割当を置換、200。Ownerの割当変更は専用の操作で扱う |

割当は招待と同じrole・all_hosts・host_id形式。非Owner固定ロールのみ付与可能で、操作者の最新権限の部分集合であることを検証する。グループにユーザーを追加する際も、そのグループから得るすべての権限を検証する。入れ子グループとグループ経由のOwnerは許可しない。存在しないグループ・ユーザーは全体認可後に404。

所属・割当・有効性の変更では影響を受けるユーザーのauthorization_versionを更新し、次の要求から新しい権限を使用する。`GET /me` に自分のauthorization_versionを返す。変更対象の未使用発行招待を取り消し、全セッションの再認証時刻をクリアする。これにより、権限の剥奪と再付与を経て古い招待・再認証が復活することを防ぐ。通常セッションは有効なまま最新権限で判定する。

## Ownerの管理

| メソッド・パス | 本文・結果 |
|---|---|
| PUT /users/{id}/owner | enabled（必須bool）。trueで直接・全体のOwner割当を追加、falseで解除。200 |
| POST /ownership/transfer | user_id。操作者のOwnerを指定ユーザーへ移譲。200 |

両方とも有効なOwner、同じセッションの直近5分以内の再認証、Origin・CSRFを必要とする。通常の割当編集とグループ経由でOwnerを変更することはできない。追加先・移譲先は有効な既存ユーザーに限定し、無効ユーザーは409、不存在はOwner認可後に404。最後の有効Ownerの解除は409。自己への移譲も409。

Owner追加・解除では他の非Owner割当を維持する。状態が既に要求どおりの場合は変更せず200（追加先が無効な場合は409）。実際にOwner割当が変わると、対象の版更新・未使用発行招待取消と全セッション失効を同時に行う。対象ユーザーはログインし直す必要がある。

移譲は、移譲先へのOwner付与、移譲元のOwner解除と全体Adminの直接付与、両者の版更新・未使用招待取消・全セッション失効・監査を原子的に行う。移譲元もログインし直す必要があり、応答で操作者のCookieを削除する。途中で失敗した場合は、権限・セッション・監査の一部だけが確定しない。

## Web画面の配信

`GET /`、`/setup`、`/login`、`/invite`、`/hosts`、`/activity`、`/users`、`/settings` は画面の共通HTMLを返し、`/assets/*` と `/favicon.png` はビルド済みファイルを返す。いずれも認証不要で、データを含まない。データの取得・変更は上記のAPIで認可する。これ以外のパスは従来どおり404で、`http.reject` を監査する。

HTMLの応答は `Cache-Control: no-store` と、応答ごとのnonceを含むCSPを返す。`/assets/` のファイル名は内容のハッシュを含むため、長期キャッシュを許可する。画面を含めずにビルドしたバイナリは、画面のページに503と案内文を返す。

## Agentタスク配送とsystemd状態

POST /agent/v1/tasks/poll は {} で資格情報の所属ホストのタスクを取得する。応答のtaskは未配送がなければnull。配送はid、attempt_id、input_hash、operation=collect、accept_deadline、run_deadlineを含む。受付確認は {"receipt":{"id":"...","attempt_id":"...","input_hash":"..."}} を送る。

POST /agent/v1/tasks/{id}/result はid、attempt_id、input_hash、outcome（succeeded/failed/unknown）を送り、acknowledged=trueが受領確認となる。同一結果の再送は成功し、異なる結果は409、別ホストの試行は404。受付期限切れの新規配送は行わない。実行期限後の結果はunknownとして保持する。状態遷移・監査は同じトランザクションで確定する。

POST /agent/v1/systemd はcollected_at、available、truncated、units（unit/load/active/sub）を送る。所属ホストは資格情報から決定する。GET /api/v1/hosts/{id}/systemd は親ホストのhost.read範囲に限定し、received_atを加えた最終状態、未受信ならnullを返す。失効後の書込みは拒否する。変更操作は提供しない。
タスク結果の受領応答は acknowledged に加えて state と finished_at（未確定ならnull）を返す。Agentはこの状態と時刻を保持の判断に使い、サーバーでunknownとなった結果を終端として清掃しない。heartbeatのtasks capabilityは、新規タスクを受領できる間だけ送る。満杯などで受領できない場合は次のheartbeatから外れ、空きが戻れば復帰する。
