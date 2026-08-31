$ErrorActionPreference = 'Stop'
$host_ = '127.0.0.1'
$port_ = '3999'
$base = "http://${host_}:${port_}"

Write-Host '--- build ---'
go build -o briefly_test.exe .
if ($LASTEXITCODE -ne 0) { throw 'build failed' }

$env:BRIEFLY_PORT = $port_
$p = Start-Process -FilePath .\briefly_test.exe -PassThru -WindowStyle Hidden -RedirectStandardError NUL
Start-Sleep -Seconds 4

try {
  $uname = 'qrtest_' + (Get-Random -Maximum 99999)
  Write-Host "user: $uname"

  $sv = New-Object Microsoft.PowerShell.Commands.WebRequestSession

  Write-Host '--- login ---'
  $login = Invoke-WebRequest -Uri "$base/api/auth/login" -Method Post -ContentType 'application/json' -Body (@{username='aezqsm';password='213311dinA_'} | ConvertTo-Json -Compress) -WebSession $sv -UseBasicParsing
  Write-Host "login: $($login.StatusCode) $($login.Content)"

  Write-Host '--- create QR session (PC, no auth) ---'
  $s = Invoke-WebRequest -Uri "$base/api/auth/qr/session" -Method Post -UseBasicParsing
  $tok = ($s.Content | ConvertFrom-Json).token
  Write-Host "token: $tok"

  Write-Host '--- poll before claim ---'
  $poll0 = Invoke-WebRequest -Uri "$base/api/auth/qr/poll?token=$tok" -UseBasicParsing
  Write-Host "poll0: $($poll0.Content)"

  Write-Host '--- claim from phone (with session) ---'
  $claim = Invoke-WebRequest -Uri "$base/api/auth/qr/claim" -Method Post -ContentType 'application/json' -Body (@{token=$tok} | ConvertTo-Json -Compress) -WebSession $sv -UseBasicParsing
  Write-Host "claim: $($claim.Content)"

  Start-Sleep -Milliseconds 500

  Write-Host '--- poll after claim ---'
  $poll = Invoke-WebRequest -Uri "$base/api/auth/qr/poll?token=$tok" -UseBasicParsing
  Write-Host "poll: $($poll.Content)"
  $code = ($poll.Content | ConvertFrom-Json).exchange_code
  Write-Host "exchange_code: $code"

  Write-Host '--- exchange (PC gets session) ---'
  $ex = Invoke-WebRequest -Uri "$base/api/auth/qr/exchange" -Method Post -ContentType 'application/json' -Body (@{code=$code} | ConvertTo-Json -Compress) -UseBasicParsing
  Write-Host "exchange: $($ex.Content)"
  Write-Host "Set-Cookie: $($ex.Headers['Set-Cookie'])"

  Write-Host '--- verify session works ---'
  $me = Invoke-WebRequest -Uri "$base/api/auth/me" -WebSession (New-Object Microsoft.PowerShell.Commands.WebRequestSession) -UseBasicParsing
  Write-Host "me (no cookie): $($me.Content)"
}
finally {
  Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
  Start-Sleep 1
  Remove-Item .\briefly_test.exe -Force -ErrorAction SilentlyContinue
}
Write-Host DONE