# Windowsへの導入

Windows amd64のControl PlaneとAgentは、NTFS上でWindowsサービスとして動作します。Windows 11・Windows Server 2025を対象とします。Windowsサービスの登録とアクセス権の設定には、管理者として起動した64ビットPowerShell 5.1以降を使用します。WSLは不要です。開発途中のため、本番利用はお控えください。

v0.6のファイル管理を有効にする設定と専用ディレクトリの権限は[FILES.md](FILES.md)を参照してください。新規AgentサービスのInstallには`-FileRoots`を指定できます。省略時はファイル管理を無効にします。

## 配布ファイルの確認

Windows用ZIPとSHA256SUMSを取得し、`Get-FileHash -Algorithm SHA256 .\amuos_*_windows_amd64.zip` の値を照合してから展開します。以下では展開先を `C:\AmuOS-release` とします。Linux用tar.gzとは別の配布物です。

サービスの実行アカウントは `NT SERVICE\AmuOSControlPlane` と `NT SERVICE\AmuOSAgent` です。管理者権限やパスワードは与えません。AgentにはPerformance Monitor Usersグループによる監視カウンターの読み取り権限を付けます。

既定の専用ディレクトリは `%ProgramData%\AmuOSControlPlane` と `%ProgramData%\AmuOSAgent` です。SYSTEM・Administratorsと、そのサービスだけがアクセスできます。サービスは自身の状態ディレクトリに書き込み、本体・設定・TLS鍵は読み取りだけです。別の場所へ導入する場合も、既存データのない専用のローカルNTFSディレクトリを指定してください。ネットワーク共有とreparse pointは使用できません。通常ユーザーに書き込みを許可した場所に本体や状態を置かないでください。

## Control Plane

利用するホスト名に対するHTTPS証明書とPEM形式の秘密鍵を用意します。秘密鍵の元ファイルも管理者だけが読める場所に保管してください。サンプルはローカル待受です。LANから接続する場合はアドレス・Origin・証明書を合わせ、必要なポートだけをWindows Defender Firewallで許可します。スクリプトはファイアウォールを変更しません。

```powershell
Set-Location C:\AmuOS-release
.\packaging\Manage-AmuOS.ps1 -Action Install -Component ControlPlane -Package $PWD `
  -Certificate C:\Private\server.pem -PrivateKey C:\Private\server-key.pem `
  -Server https://localhost:8443 -Address 127.0.0.1:8443 -AgentAddress 127.0.0.1:9443
Start-Service AmuOSControlPlane
Get-Content "$env:ProgramData\AmuOSControlPlane\state\setup-token"
```

ブラウザーでOriginのURLを開き、10分以内に初回設定を完了します。終わったら `state\setup-token` を削除します。サービスは自動起動に設定されます。稼働状態は `Get-Service AmuOSControlPlane`、起動・停止・エラー・Agentの警告はイベントビューアーの「Windowsログ → Application」で確認できます（ソース名はサービス名）。秘密値をイベントログに貼り付けないでください。

## Agent

ホスト画面でホストと一回限りの登録コードを作成します。コードを、一般ユーザーには読めないファイル（例: `C:\Private\agent-code`）に保存します。`ca.pem` はControl PlaneのHTTPS証明書を検証するCA証明書です。自己署名証明書を使うローカル試験では、その証明書を指定します。

```powershell
.\packaging\Manage-AmuOS.ps1 -Action Install -Component Agent -Package $PWD `
  -CAFile C:\Private\ca.pem -Server https://localhost:8443 -AgentServer https://localhost:9443
$root = "$env:ProgramData\AmuOSAgent"
& "$root\bin\amuos-agent.exe" --state-dir "$root\state" `
  --server https://localhost:8443 --agent-server https://localhost:9443 `
  --ca-file "$root\ca.pem" --enroll-code-file C:\Private\agent-code
Remove-Item -LiteralPath C:\Private\agent-code
Start-Service AmuOSAgent
```

CPU、物理メモリ、システムドライブの使用率、固定ドライブの容量、ディスクI/O、物理ネットワークの転送量、ホスト名・カーネル版・CPUモデル・論理CPU数・メモリ容量・インターフェースを収集します。ディスクI/OはWindowsのPhysicalDiskカウンターを使用します。カウンターが無効・利用不能なときは欠測です。初回のCPU・転送量も欠測となります。64論理CPUを超えるホストのCPU使用率は、全プロセッサーグループの収集に対応するまで欠測です。Load、Linuxと同じ意味でのSwap、起動時刻、systemdの状態取得は対応していません。画面ではsystemd未対応を明示し、未取得の数値を0として表示しません。Windowsサービス自体の一覧・操作は提供しません。

## 停止・再起動

`Stop-Service` / `Start-Service` / `Restart-Service` を使います。停止時は通信とDBの処理を終了してからサービスを停止します。Agentの資格情報とタスク記録はstate内に残り、再起動後に使用されます。サービス名を変えたり、SCMに登録されたコマンドラインを相対パスへ変えたりしないでください。

## バックアップ・更新

まずControl Planeの本体で回復鍵を作り、表示された公開recipientを記録し、鍵ファイルは別の安全な場所に保管します。鍵は初回作成時からNTFSの継承を無効にして、作成者・SYSTEM・Administratorsだけにアクセスを許可します。

```powershell
$cp = "$env:ProgramData\AmuOSControlPlane"
& "$cp\bin\amuos-controlplane.exe" --generate-recovery-key C:\Private\recovery.key
```

更新版ZIPを別の場所に展開し、その版のスクリプトで更新します。稼働中に本体やDBを直接上書きしないでください。

```powershell
C:\AmuOS-new\packaging\Manage-AmuOS.ps1 -Action Update -Component ControlPlane `
  -Package C:\AmuOS-new -BackupRecipient age1...
C:\AmuOS-new\packaging\Manage-AmuOS.ps1 -Action Update -Component Agent -Package C:\AmuOS-new
```

Control Planeは停止→新しい本体でDB互換性確認→旧版で暗号化バックアップ→本体差し替え→起動の順です。旧本体・設定・更新前バックアップは専用ディレクトリの `previous` に残します。Agentも停止して本体を保存し、差し替えます。状態・設定・鍵は引き継ぎます。失敗した場合は自動でDBを戻さず、停止状態で診断します。更新後の動作を確認してから `previous` を安全な別の場所へ保管してください。既存の `previous` がある間は次の更新を拒否します。

## 切り戻し・復元

```powershell
C:\AmuOS-new\packaging\Manage-AmuOS.ps1 -Action Rollback -Component Agent
C:\AmuOS-new\packaging\Manage-AmuOS.ps1 -Action Rollback -Component ControlPlane `
  -RecoveryKeyFile C:\Private\recovery.key
```

Control Planeの切り戻しは、旧本体と更新前DBの組を使います。更新以降の変更は失われます。復元時には全セッションとAgent資格情報を失効させ、Agent CAも作り直すため、各Agentを新しいコードで再登録してください。一般的な暗号化バックアップ・同版復元の制約は同梱のBACKUP.mdもご覧ください。CLIを直接使う場合は、サービスを止め、専用state内のDBを指定します。別のマシンへ復元するときは、先に導入スクリプトで専用アカウントとACLを用意し、停止中のサービスのDBへ復元します。

## アンインストール

```powershell
.\packaging\Manage-AmuOS.ps1 -Action Uninstall -Component Agent
.\packaging\Manage-AmuOS.ps1 -Action Uninstall -Component ControlPlane
```

サービスを停止・削除し、Agentの監視用グループ所属を解除します。本体・状態・鍵・バックアップとイベントログは残します。必要なバックアップを別の安全な場所へ移したあと、専用ディレクトリの絶対パスを確かめてから管理者が削除してください。
