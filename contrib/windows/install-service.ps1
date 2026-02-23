#!/usr/bin/env pwsh
# install-service.ps1 — register suffuse as a Windows service using sc.exe
# Run as Administrator.
#
# Usage:
#   .\install-service.ps1
#   .\install-service.ps1 -BinPath "C:\Program Files\suffuse\suffuse.exe" `
#                         -Token "changeme"
#
# For a federated node that connects to another server:
#   .\install-service.ps1 -Token "changeme" `
#                         -UpstreamHost "hub.example.com" `
#                         -UpstreamPort 8752

param(
    [string]$BinPath      = "C:\Program Files\suffuse\suffuse.exe",
    [string]$Token        = "",
    [string]$Addr         = "0.0.0.0:8752",
    [string]$UpstreamHost = "",
    [int]$UpstreamPort    = 8752
)

$ErrorActionPreference = "Stop"

function Register-SuffuseService {
    param($Name, $DisplayName, $Description, $Args)

    $existing = Get-Service -Name $Name -ErrorAction SilentlyContinue
    if ($existing) {
        Write-Host "Removing existing service: $Name"
        Stop-Service -Name $Name -Force -ErrorAction SilentlyContinue
        sc.exe delete $Name | Out-Null
        Start-Sleep -Seconds 2
    }

    sc.exe create $Name `
        binPath= "`"$BinPath`" $Args" `
        DisplayName= $DisplayName `
        start= auto | Out-Null

    sc.exe description $Name $Description | Out-Null
    sc.exe failure $Name reset= 60 actions= restart/5000/restart/5000/restart/10000 | Out-Null

    Write-Host "Created service: $Name"
}

# ── Suffuse Server ────────────────────────────────────────────────────────────

$serverArgs = "server --addr `"$Addr`" --log-format json"
if ($Token)        { $serverArgs += " --token `"$Token`"" }
if ($UpstreamHost) {
    $serverArgs += " --upstream-host `"$UpstreamHost`" --upstream-port $UpstreamPort"
}

Register-SuffuseService `
    -Name        "SuffuseServer" `
    -DisplayName "Suffuse Clipboard Hub" `
    -Description "Suffuse shared-clipboard hub. Distributes clipboard events to all connected peers." `
    -Args        $serverArgs

Write-Host ""
Write-Host "Service registered. Start with:"
Write-Host "  Start-Service SuffuseServer"
Write-Host ""
Write-Host "Or start immediately:"
Write-Host "  Start-Service SuffuseServer"
Write-Host ""
Write-Host "Logs: Event Viewer > Windows Logs > Application (source: SuffuseServer)"
Write-Host "      or: Get-EventLog -LogName Application -Source SuffuseServer -Newest 50"
Write-Host ""
if ($Token) {
    Write-Host "All peers must use token: $Token"
} else {
    Write-Host "No token set — using default 'suffuse'. Set -Token for a private network."
}

# ── Config file ───────────────────────────────────────────────────────────────
# Create the ProgramData config directory if it doesn't exist
$configDir = "$env:ProgramData\suffuse"
if (-not (Test-Path $configDir)) {
    New-Item -ItemType Directory -Path $configDir | Out-Null
    Write-Host ""
    Write-Host "Config directory created: $configDir"
    Write-Host "Place suffuse.toml there to configure the service."
    Write-Host "(The service account needs read access to this directory.)"
}
