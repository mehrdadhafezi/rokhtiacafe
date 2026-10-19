<#
.SYNOPSIS
  Local development commands for Rokh Tia Café (Windows / PowerShell 7).

.EXAMPLE
  ./scripts/dev.ps1 setup     # create .env, start PostgreSQL, apply migrations
  ./scripts/dev.ps1 run       # run the API server
  ./scripts/dev.ps1 check     # gofmt + vet + tests (incl. PostgreSQL integration) + sqlc diff
  ./scripts/dev.ps1 fe-dev    # frontend dev server on http://127.0.0.1:5173 (proxies /api to :8080)
  ./scripts/dev.ps1 fe-check  # frontend typecheck + lint + tests + build + PWA check (same as CI)
#>
param(
    [Parameter(Position = 0)]
    [ValidateSet('setup', 'db-up', 'db-down', 'db-reset', 'migrate', 'status', 'run', 'build', 'fmt', 'vet', 'test', 'sqlc', 'sqlc-diff', 'smoke', 'check',
        'fe-install', 'fe-dev', 'fe-test', 'fe-build', 'fe-check', 'help')]
    [string]$Command = 'help'
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$Backend = Join-Path $Root 'backend'
$Frontend = Join-Path $Root 'frontend'
$SqlcImage = 'sqlc/sqlc:1.29.0'

# Use the verified Go install even if this shell's PATH is stale.
if (-not (Get-Command go -ErrorAction SilentlyContinue) -and (Test-Path 'D:\Apps\bin\go.exe')) {
    $env:Path = "D:\Apps\bin;$env:Path"
}

function Invoke-Step([string]$Name, [scriptblock]$Block) {
    Write-Host "==> $Name" -ForegroundColor Cyan
    & $Block
    if ($LASTEXITCODE -ne 0) { throw "$Name failed (exit $LASTEXITCODE)" }
}

function In-Backend([scriptblock]$Block) {
    Push-Location $Backend
    try { & $Block } finally { Pop-Location }
}

function In-Frontend([scriptblock]$Block) {
    Push-Location $Frontend
    try {
        if (-not (Test-Path 'node_modules')) { Invoke-Step 'npm ci' { npm ci } }
        & $Block
    } finally { Pop-Location }
}

function Ensure-Env {
    $envFile = Join-Path $Root '.env'
    if (-not (Test-Path $envFile)) {
        Copy-Item (Join-Path $Root '.env.example') $envFile
        Write-Host 'Created .env from .env.example (local development defaults).'
    }
}

function Db-Up { Invoke-Step 'start PostgreSQL' { docker compose -f (Join-Path $Root 'docker-compose.yml') up -d --wait } }

function Sqlc([string]$Sub) {
    Invoke-Step "sqlc $Sub" { docker run --rm -v "${Backend}:/src" -w /src $SqlcImage $Sub }
}

function Smoke {
    Ensure-Env
    In-Backend { Invoke-Step 'build' { go build -o bin/rokh.exe ./cmd/rokh } }
    $exe = Join-Path $Backend 'bin/rokh.exe'
    $proc = Start-Process -FilePath $exe -ArgumentList 'serve' -WorkingDirectory $Backend -PassThru -NoNewWindow
    try {
        $addr = 'http://127.0.0.1:8080'
        $ok = $false
        for ($i = 0; $i -lt 50; $i++) {
            try { Invoke-RestMethod "$addr/healthz" | Out-Null; $ok = $true; break } catch { Start-Sleep -Milliseconds 200 }
        }
        if (-not $ok) { throw 'server did not start' }
        $h = Invoke-RestMethod "$addr/healthz"
        $r = Invoke-RestMethod "$addr/readyz"
        Write-Host "healthz: $($h.status)  readyz: $($r.status)" -ForegroundColor Green
        if ($h.status -ne 'ok' -or $r.status -ne 'ready') { throw 'smoke test failed' }
    } finally {
        Stop-Process -Id $proc.Id -ErrorAction SilentlyContinue
    }
}

switch ($Command) {
    'setup'     { Ensure-Env; Db-Up; In-Backend { Invoke-Step 'migrate up' { go run ./cmd/rokh migrate up } } }
    'db-up'     { Db-Up }
    'db-down'   { Invoke-Step 'stop PostgreSQL' { docker compose -f (Join-Path $Root 'docker-compose.yml') down } }
    'db-reset'  { Invoke-Step 'drop local database volume' { docker compose -f (Join-Path $Root 'docker-compose.yml') down -v }; Db-Up }
    'migrate'   { Ensure-Env; In-Backend { Invoke-Step 'migrate up' { go run ./cmd/rokh migrate up } } }
    'status'    { Ensure-Env; In-Backend { Invoke-Step 'migrate status' { go run ./cmd/rokh migrate status } } }
    'run'       { Ensure-Env; In-Backend { go run ./cmd/rokh serve } }
    'build'     { In-Backend { Invoke-Step 'build' { go build -o bin/rokh.exe ./cmd/rokh } } }
    'fmt'       { In-Backend { Invoke-Step 'gofmt' { gofmt -w . } } }
    'vet'       { In-Backend { Invoke-Step 'go vet' { go vet ./... } } }
    'test'      { Ensure-Env; In-Backend { Invoke-Step 'go test' { go test -count=1 ./... } } }
    'sqlc'      { Sqlc 'generate' }
    'sqlc-diff' { Sqlc 'diff' }
    'smoke'     { Smoke }
    'check' {
        Ensure-Env
        In-Backend {
            Write-Host '==> gofmt' -ForegroundColor Cyan
            $unformatted = gofmt -l .
            if ($unformatted) { throw "gofmt needed:`n$unformatted" }
            Invoke-Step 'go vet' { go vet ./... }
            Invoke-Step 'go build' { go build ./... }
            $env:REQUIRE_DB_TESTS = '1'
            Invoke-Step 'go test' { go test -count=1 ./... }
        }
        Sqlc 'diff'
        Write-Host 'All checks passed.' -ForegroundColor Green
    }
    'fe-install' { Push-Location $Frontend; try { Invoke-Step 'npm ci' { npm ci } } finally { Pop-Location } }
    'fe-dev'     { In-Frontend { npm run dev } }
    'fe-test'    { In-Frontend { Invoke-Step 'vitest' { npm test } } }
    'fe-build'   { In-Frontend { Invoke-Step 'vite build' { npm run build } } }
    'fe-check' {
        In-Frontend {
            Invoke-Step 'typecheck' { npm run typecheck }
            Invoke-Step 'eslint' { npm run lint -- --max-warnings=0 }
            Invoke-Step 'vitest' { npm test }
            Invoke-Step 'vite build' { npm run build }
            Invoke-Step 'PWA separation check' { npm run check:pwa }
        }
        Write-Host 'Frontend checks passed.' -ForegroundColor Green
    }
    default { Get-Help $PSCommandPath -Detailed }
}
