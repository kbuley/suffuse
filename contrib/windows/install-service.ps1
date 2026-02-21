# install-service.ps1 — register suffuse as Windows services using sc.exe
# Run as Administrator.

param(
    [string]$BinPath = "C:\Program Files\suffuse\suffuse.exe",
    [string]$Token   = "",
    [string]$Addr    = ":9876",
    [string]$Server  = "localhost:9876"
)

$ErrorActionPreference = "Stop"

function Register-SuffuseService {
    param($Name, $DisplayName, $Args, $Description)

    $existing = Get-Service -Name $Name -ErrorAction SilentlyContinue
    if ($existing) {
        Write-Host "Removing existing service: $Name"
        Stop-Service -Name $Name -Force -ErrorAction SilentlyContinue
        sc.exe delete $Name | Out-Null
        Start-Sleep -Seconds 2
    }

    $binPathWithArgs = "`"$BinPath`" $Args"
    sc.exe create $Name `
        binPath= $binPathWithArgs `
        DisplayName= $DisplayName `
        start= auto | Out-Null

    sc.exe description $Name $Description | Out-Null
    sc.exe failure $Name reset= 60 actions= restart/5000/restart/5000/restart/10000 | Out-Null

    Write-Host "Created service: $Name"
}


$serverArgs = "server --addr `"$Addr`""
if ($Token) { $serverArgs += " --token `"$Token`"" }

Register-SuffuseService `
    -Name "SuffuseServer" `
    -DisplayName "Suffuse Clipboard Hub" `
    -Args $serverArgs `
    -Description "Suffuse shared clipboard hub. Distributes clipboard content to all connected clients."

$clientArgs = "client --server `"$Server`""
if ($Token) { $clientArgs += " --token `"$Token`"" }

Register-SuffuseService `
    -Name "SuffuseClient" `
    -DisplayName "Suffuse Clipboard Client" `
    -Args $clientArgs `
    -Description "Suffuse shared clipboard client. Syncs the local clipboard with the hub."

Write-Host ""
Write-Host "Services registered. Start with:"
Write-Host "  Start-Service SuffuseServer"
Write-Host "  Start-Service SuffuseClient"
Write-Host ""
Write-Host "Logs: Event Viewer > Windows Logs > Application (source: suffuse)"
