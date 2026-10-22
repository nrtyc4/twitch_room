# Smoke-test the twitch-room backend against an in-memory store.
# Requires: go, curl.exe. Run from the repo root in PowerShell.
$ErrorActionPreference = "Continue"
$Port = if ($env:PORT) { $env:PORT } else { "8080" }
$Base = "http://localhost:$Port"
$Bin = Join-Path $env:TEMP "twitch-room-smoke.exe"
$RunDir = Join-Path $env:TEMP "twitch-room-smoke"
$Token = "dev-smoke-token"

New-Item -ItemType Directory -Force -Path $RunDir | Out-Null
Write-Host "Building..."
go build -o $Bin .
if ($LASTEXITCODE -ne 0) { throw "build failed" }

Write-Host "Starting server (in-memory)..."
$env:DATABASE_DSN = ""
$env:APP_ENV = ""
$env:ALLOW_DEV_AUTH = "true"
$env:PORT = $Port
$env:TWITCH_ROOM_AUTH_TOKEN = $Token
$env:ALLOWED_ORIGINS = "*"
$proc = Start-Process -FilePath $Bin -WorkingDirectory $RunDir -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 2

$fail = 0
function Invoke-Check {
    param([string]$Name, [string[]]$CurlArgs)
    $code = & curl.exe -s -o "$env:TEMP\smoke-body" -w "%{http_code}" @CurlArgs
    Write-Host "[$code] $Name"
    if ([int]$code -ge 400) {
        $script:fail = 1
        Get-Content "$env:TEMP\smoke-body" | Write-Host
    }
}

$auth = @("-H", "Authorization: Bearer $Token", "-H", "X-User-ID: alice")
$bob = @("-H", "Authorization: Bearer $Token", "-H", "X-User-ID: bob")

Write-Host "--- smoke ---"
Invoke-Check "health" @("$Base/health")
Invoke-Check "twitch client id" @("$Base/auth/twitch/client-id")
Invoke-Check "metrics (authed)" @("-H", "Authorization: Bearer $Token", "$Base/metrics")
$jsonPublish = '{"stream_ids":["shroud"]}'
$jsonPublish | Set-Content -NoNewline -Path "$env:TEMP\smoke-publish.json"
Invoke-Check "publish alice" (@("-X", "POST", "$Base/v1/watch", "-H", "Content-Type: application/json") + $auth + @("-d", "@$env:TEMP\smoke-publish.json"))
Invoke-Check "read alice" (@("$Base/v1/users/alice/watch") + $auth)
Invoke-Check "settings alice" (@("$Base/v1/settings") + $auth)
Invoke-Check "subscribe bob->alice" (@("-X", "POST", "$Base/v1/subscriptions/alice") + $bob)
Invoke-Check "feed bob" (@("$Base/v1/feed") + $bob)

$jsonSettings = '{"subscription_mode":"open","visibility":"public"}'
$jsonSettings | Set-Content -NoNewline -Path "$env:TEMP\smoke-settings.json"
& curl.exe -s -X PUT "$Base/v1/settings" -H "Content-Type: application/json" @auth -d "@$env:TEMP\smoke-settings.json" | Out-Null
$code = & curl.exe -s -o "$env:TEMP\smoke-body" -w "%{http_code}" "$Base/u/alice"
Write-Host "[$code] share page (public)"
if ($code -ne "200") { $fail = 1; Get-Content "$env:TEMP\smoke-body" | Write-Host }

Invoke-Check "delete alice data" (@("-X", "DELETE", "$Base/v1/account") + $auth)

Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue

if ($fail -eq 0) { Write-Host "SMOKE TEST: PASS"; exit 0 }
else { Write-Host "SMOKE TEST: FAIL"; exit 1 }