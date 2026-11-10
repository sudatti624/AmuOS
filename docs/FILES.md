# ファイル管理（v0.6）

設計判断はADR-027。Linux/WindowsのAgentで、管理者が用意した専用ディレクトリだけを公開する。Agentの`--file-roots`はJSON設定ファイルを指定する。例：

```json
{"workspace":{"path":"/srv/amuos-files","read_only":false}}
```

Windowsでは`path`を`C:\\AmuOSFiles`などにし、許可ディレクトリにもローカルNTFSを使用する。省略時はファイル操作を無効にする。ディレクトリは事前作成し、Agentの実行ユーザーと信頼する管理者だけにOSの書込み権限を与える。他プロセスによる同時変更、マウントの付替え、管理者によるハードリンクの持込みは対象外。Agent状態、Control Plane状態、証明書・秘密鍵・設定やそれらの親ディレクトリを指定しない。Agent状態と重なるルートは起動時に拒否する。Control Planeの状態を置くディレクトリとはOSユーザーも分離する。

ホスト詳細のファイル設定で同じルート名を登録し、閲覧・変更を許すロールを選ぶ。ホストの権限とディレクトリの許可ロールは同じ割当で照合する。OwnerもAgent側の許可範囲・読取り専用・OS権限を超えられない。Viewerには変更を許可しない。設定変更はホストAdmin以上が行う。

単一ファイルは256 KiB、一覧は1,000件まで。転送はJSONのbase64で行い、実行前に全量を受信する。テキスト編集はUTF-8に限る。相対パスのみを受け付け、リンク・特殊ファイル・Windowsの代替データストリーム・予約名を拒否する。Goのos.Rootでディレクトリ外への差替えを防ぐ。ルート自体の移動やローカル管理者による変更を防ぐ隔離機構ではない。

書込みは同じディレクトリの一時ファイルを同期してから公開する。更新・移動・削除には取得済みETagと確認が必要。編集前・削除前の通常ファイルはルート直下の非公開`.amuos-history`に退避する。ディレクトリ削除は空の場合のみ。退避ファイルの復旧・保持容量管理はホスト管理者がAgent停止中に行う。退避領域の`<ID>.json`に元の相対パス、`<ID>.data`に変更前の内容がある。復旧先と現物を確認してから手動で戻す。退避領域をUIから公開しない。容量不足は失敗として扱い、未完成転送を完成名で公開しない。プロセス停止後の`.amuos-upload-*`は管理者が確認して削除できる。

操作は既存の永続タスクで受付・配送・結果を監査する。再起動時の未確定操作はunknownとし、自動再実行しない。fileタスクは汎用retryを拒否し、現物の状態を再取得して新しいキーで要求する。通信再送は同じキー・入力を再生する。権限・ルート設定は受付、配送、結果取得時に再確認する。Agentは実行直前にもローカル設定を確認する。配送済みの操作は権限剥奪で巻き戻らない。

内容は監査ログに残さない。転送内容は保護されたDBとAgentのタスクジャーナルに保持され、確定済みタスクは通常タスクと同じ30日保持の対象になる。unknownは確認・解決まで保持する。結果は依頼者のみ取得可能。管理基盤バックアップには転送内容も含まれるが、実際のルート全体と退避領域は含まれない。ホスト側データは別にバックアップする。v0.5への切戻しには変更前の基盤バックアップを使う。

## 常駐Agentで有効にする

Linuxでは、専用ディレクトリを`amuos-agent`所有の0700で作り、設定を`/etc/amuos-agent/file-roots.json`に置く。systemdのoverrideで次を追加する（サービス定義の他の保護設定は維持する）。

```ini
[Service]
ExecStart=
ExecStart=/usr/local/bin/amuos-agent --state-dir /var/lib/amuos-agent --server ${AMUOS_SERVER} --agent-server ${AMUOS_AGENT_SERVER} --ca-file /etc/amuos-agent/server-ca.pem --file-roots /etc/amuos-agent/file-roots.json
ReadWritePaths=/srv/amuos-files
```

`systemctl daemon-reload`後にAgentを再起動する。読取り専用なら`ReadOnlyPaths`とローカル設定の`read_only:true`を使う。公開先は`ProtectHome`の対象外に用意する。通常のサービスはファイル管理無効のまま。

Windowsの新規サービス導入では`Manage-AmuOS.ps1 -Action Install -Component Agent ... -FileRoots <設定ファイル>`を指定できる。既存サービスは停止後、保存されたサービス引数に`--file-roots <設定ファイル>`を追加し、SCMの実行引数と`service.json`のArgumentsを揃えて再起動する。ルートには`NT SERVICE\AmuOSAgent`のModify権限と管理者のFullControlだけを与え、親の一般ユーザー向け継承を無効にする。Control Planeの状態ディレクトリへの権限をAgentへ与えない。
