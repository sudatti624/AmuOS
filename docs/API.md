# API契約（実装済みの最初の単位）

基準: 仕様v0.4 §10、§12。HTTPS JSON、ルート `/api/v1`。秘密値をクエリパラメーターに含めない。

| メソッド・パス | 入力 | 認可・結果 |
|---|---|---|
| GET /healthz | なし | 公開、生存確認のみ。DBや依存先のreadinessを保証しない |
| POST /setup | token, username, password | ローカル発行の10分・一回限り資格情報。201 |
| POST /auth/login | username, password | 有効ユーザーとパスワード。200とCookie、csrf_token |
| POST /auth/logout | 本文不要、X-CSRF-Token | 有効セッション、Origin・CSRF一致。200、セッション削除 |
| GET /me | Cookie | 有効セッション。idと最新の範囲付き割当 |
| GET /hosts | Cookie | 最新のhost.read範囲のみ。空の場合はitems: []。削除済みは非表示 |
| POST /hosts | Cookie、name、X-CSRF-Token | 全体host.manageのみ。pending IDを確保、201。Agent登録・実測値ではない |
| GET /audit | Cookie | audit.read範囲のみ、最新100件まで。全体イベントには全体権限が必要 |

変更要求・ログイン・初期設定に設定値と完全一致のOriginヘッダーを必須とする。CSRFトークンはログイン応答時にのみ返す。紛失時は再ログインする。Cookieは `__Host-amuos_session`、Secure/HttpOnly/SameSite=Strict、Path=/、Domainなし。アイドル30分・絶対12時間。OriginのないCLIも設定値を明示して送信する必要がある。

パスワード12〜128バイト。usernameは小文字化した3〜64文字の英数字・`_-.`。本文16KiB、未知フィールド・複数JSON・不正JSONを拒否する。認証系は送信元IPあたり15分に5回まで（成功・不正入力を含む）、X-Forwarded-Forは使わない。同時パスワード計算は1件までに制限する。

| HTTP | エラー |
|---|---|
| 400 | invalid_input / invalid_json |
| 401 | unauthorized（未認証・無効・期限切れを区別しない） |
| 403 | forbidden / origin_rejected |
| 409 | conflict（初期化済み） |
| 415 | json_required |
| 429 | rate_limited、Retry-After付き |
| 500 | internal_error（DB・監査等の詳細を外部へ公開しない） |
| 503 | auth_busy、Retry-After付き |

セッション認証と最新割当の取得・状態更新・監査は同じトランザクション。Origin/JSON形式の不正はDB処理前に拒否する。これらHTTP入口での拒否とレート制限の監査連携は未実装。招待・グループ変更・ロール割当・Owner移譲・ホスト削除・Agent・タスク・バックアップAPIは未公開。作成APIの永続冪等キーも未実装であり、結果不明時は自動再送しない。
