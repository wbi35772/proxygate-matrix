# ProxyGate-Matrix -> local Windows proxy nodes
#
# Reads the allocated matrix slots from the Instabox sandbox over SSH, keeps one
# SSH tunnel with every slot port forwarded, probes each exit, and writes
# Clash/Mihomo, sing-box and SOCKS5 node lists to -OutDir.
#
# Example:
#   $env:PROXYGATE_SSH = "ibssh_xxx@ssh.instabox.sh"
#   .\scripts\matrix-win.ps1
#   .\scripts\matrix-win.ps1 -Allocate 10 -Country JP
#   .\scripts\matrix-win.ps1 -SkipCheck -WithPanel

[CmdletBinding()]
param(
    [string]$SshTarget = $env:PROXYGATE_SSH,
    [int]$SshPort = 2222,
    [string]$RemoteDir = "/data/proxygate/matrix-src",
    [string]$OutDir = (Join-Path (Get-Location) "proxygate-nodes"),
    [int]$Allocate = 0,
    [string]$Country = "JP",
    [string]$Protocol = "openvpn_tcp",
    [ValidateSet("sticky", "rotating")]
    [string]$Mode = "sticky",
    [switch]$SkipCheck,
    [switch]$IncludeDead,
    [switch]$WithPanel
)

$ErrorActionPreference = "Stop"

if (-not $SshTarget) {
    throw "Set `$env:PROXYGATE_SSH='user@host' or pass -SshTarget (for example ibssh_xxx@ssh.instabox.sh)."
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$sshBase = @(
    "-p", "$SshPort",
    "-o", "StrictHostKeyChecking=accept-new",
    "-o", "ServerAliveInterval=30",
    "-o", "ServerAliveCountMax=3"
)

function Escape-Yaml([string]$value) {
    if ($null -eq $value) { return "" }
    return $value.Replace('\', '\\').Replace('"', '\"')
}

function Invoke-Remote([string]$command) {
    return (& ssh @sshBase $SshTarget $command)
}

if ($Allocate -gt 0) {
    Write-Host "Allocating $Allocate slot(s) country='$Country' protocol='$Protocol' mode='$Mode' ..."
    $alloc = "cd '$RemoteDir' && set -a && . ./.env && set +a && python3 scripts/shardx-slots.py --count $Allocate --country '$Country' --protocol '$Protocol' --mode '$Mode' --wait --wait-timeout 240 --format json >/tmp/win-alloc.json 2>/tmp/win-alloc.err; tail -n 3 /tmp/win-alloc.err"
    Invoke-Remote $alloc | ForEach-Object { Write-Host "  $_" }
}

$raw = (Invoke-Remote "sh $RemoteDir/scripts/api-get.sh /api/v1/pool/slots") -join "`n"
if (-not $raw.Trim()) { throw "The slot API returned an empty response over SSH." }
$state = $raw | ConvertFrom-Json
$ready = @($state.slots | Where-Object { $_.state -eq "ready" })
if ($ready.Count -eq 0) {
    throw "No ready slots. Allocate first: .\scripts\matrix-win.ps1 -Allocate 10 -Country JP"
}

$probe = @{}
$status = @{}
if ($SkipCheck) {
    foreach ($slot in $ready) {
        $probe[[int]$slot.slotId] = $slot.exitIp
        $status[[int]$slot.slotId] = "unchecked"
    }
} else {
    Write-Host "Probing $($ready.Count) slot exit(s) through the sandbox ..."
    $probeRaw = (Invoke-Remote "sh $RemoteDir/scripts/check-slots.sh") -join "`n"
    foreach ($line in ($probeRaw -split "`n")) {
        if ($line -match '^\s*(\d+)\s+(\d+)\s+(\S+)\s*$') {
            $id = [int]$Matches[1]
            $ip = $Matches[3]
            $probe[$id] = $ip
            if ($ip -eq "DEAD") { $status[$id] = "dead" } else { $status[$id] = "ok" }
        }
    }
}

$nodes = New-Object System.Collections.ArrayList
foreach ($slot in ($ready | Sort-Object slotId)) {
    $id = [int]$slot.slotId
    $socks = [uri]$slot.socks5
    $userInfo = $socks.UserInfo
    $parts = $userInfo.Split(':', 2)
    $user = [uri]::UnescapeDataString($parts[0])
    $pass = if ($parts.Length -gt 1) { [uri]::UnescapeDataString($parts[1]) } else { "" }

    $checked = $probe[$id]
    $alive = ($status[$id] -eq "ok")
    if (-not $alive -and -not $IncludeDead) { continue }

    $org = ($slot.asnOrg -split ' - ')[0]
    $org = ($org -replace '[^A-Za-z0-9]', '')
    if (-not $org) { $org = $slot.asn }
    $countryCode = if ($slot.countryShort) { $slot.countryShort } else { "XX" }
    $name = "pg-{0:D2}-{1}-{2}" -f $id, $countryCode, $org

    [void]$nodes.Add([pscustomobject]@{
        SlotId  = $id
        Name    = $name
        Host    = "127.0.0.1"
        Port    = [int]$slot.port
        User    = $user
        Pass    = $pass
        ExitIp  = $slot.exitIp
        Checked = $checked
        Status  = $status[$id]
        Asn     = $slot.asn
        AsnOrg  = $slot.asnOrg
    })
}

if ($nodes.Count -eq 0) {
    throw "Every ready slot failed the exit probe. Re-run with -IncludeDead to emit them anyway, or rotate the slots."
}

$pidFile = Join-Path $OutDir "proxygate-tunnel.pid"
if (Test-Path $pidFile) {
    $oldPid = (Get-Content $pidFile -ErrorAction SilentlyContinue | Select-Object -First 1)
    if ($oldPid -and ($oldPid -as [int])) {
        $old = Get-Process -Id ([int]$oldPid) -ErrorAction SilentlyContinue
        if ($old -and $old.ProcessName -eq "ssh") {
            Stop-Process -Id $old.Id -Force
            Start-Sleep -Milliseconds 300
        }
    }
}

$sshArgs = @(
    "-p", "$SshPort",
    "-N",
    "-o", "StrictHostKeyChecking=accept-new",
    "-o", "ExitOnForwardFailure=yes",
    "-o", "ServerAliveInterval=30",
    "-o", "ServerAliveCountMax=3"
)
foreach ($node in $nodes) {
    $sshArgs += @("-L", "$($node.Port):127.0.0.1:$($node.Port)")
}
if ($WithPanel) {
    $sshArgs += @("-L", "8080:127.0.0.1:8080")
}
$sshArgs += $SshTarget

$tunnel = Start-Process -FilePath "ssh" -ArgumentList $sshArgs -WindowStyle Hidden -PassThru
$tunnel.Id | Set-Content -Path $pidFile -Encoding ASCII
Start-Sleep -Seconds 3

$firstPort = $nodes[0].Port
$listening = $null
for ($attempt = 0; $attempt -lt 20; $attempt++) {
    $listening = Get-NetTCPConnection -LocalPort $firstPort -State Listen -ErrorAction SilentlyContinue
    if ($listening) { break }
    Start-Sleep -Milliseconds 500
}
if (-not $listening) {
    Write-Warning "Local port $firstPort is not listening; check the SSH target/token and whether another process owns the port."
}

$clash = New-Object System.Collections.Generic.List[string]
$clash.Add("mixed-port: 7890")
$clash.Add("allow-lan: false")
$clash.Add("mode: rule")
$clash.Add("log-level: info")
$clash.Add("proxies:")
foreach ($node in $nodes) {
    $clash.Add("  - name: `"$(Escape-Yaml $node.Name)`"")
    $clash.Add("    type: socks5")
    $clash.Add("    server: 127.0.0.1")
    $clash.Add("    port: $($node.Port)")
    $clash.Add("    username: `"$(Escape-Yaml $node.User)`"")
    $clash.Add("    password: `"$(Escape-Yaml $node.Pass)`"")
    $clash.Add("    udp: false")
}
$clash.Add("proxy-groups:")
$clash.Add("  - name: ProxyGate")
$clash.Add("    type: select")
$clash.Add("    proxies:")
foreach ($node in $nodes) {
    $clash.Add("      - `"$(Escape-Yaml $node.Name)`"")
}
$clash.Add("rules:")
$clash.Add("  - MATCH,ProxyGate")
$clashPath = Join-Path $OutDir "proxygate-clash.yaml"
$clash | Set-Content -Path $clashPath -Encoding UTF8

$provider = New-Object System.Collections.Generic.List[string]
$provider.Add("proxies:")
foreach ($node in $nodes) {
    $provider.Add("  - name: `"$(Escape-Yaml $node.Name)`"")
    $provider.Add("    type: socks5")
    $provider.Add("    server: 127.0.0.1")
    $provider.Add("    port: $($node.Port)")
    $provider.Add("    username: `"$(Escape-Yaml $node.User)`"")
    $provider.Add("    password: `"$(Escape-Yaml $node.Pass)`"")
    $provider.Add("    udp: false")
}
$providerPath = Join-Path $OutDir "proxygate-provider.yaml"
$provider | Set-Content -Path $providerPath -Encoding UTF8

$outbounds = New-Object System.Collections.ArrayList
foreach ($node in $nodes) {
    [void]$outbounds.Add([ordered]@{
        type        = "socks"
        tag         = $node.Name
        server      = "127.0.0.1"
        server_port = $node.Port
        username    = $node.User
        password    = $node.Pass
    })
}
[void]$outbounds.Add([ordered]@{
    type      = "selector"
    tag       = "proxy"
    outbounds = @($nodes | ForEach-Object { $_.Name })
    default   = $nodes[0].Name
})
$singbox = [ordered]@{
    log       = @{ level = "info" }
    inbounds  = @(@{ type = "mixed"; listen = "127.0.0.1"; listen_port = 7890 })
    outbounds = $outbounds
    route     = @{ final = "proxy" }
}
$singboxPath = Join-Path $OutDir "proxygate-singbox.json"
$singbox | ConvertTo-Json -Depth 8 | Set-Content -Path $singboxPath -Encoding UTF8

$lines = foreach ($node in $nodes) {
    "socks5://$([uri]::EscapeDataString($node.User)):$([uri]::EscapeDataString($node.Pass))@127.0.0.1:$($node.Port)#$($node.Name)"
}
$linesPath = Join-Path $OutDir "proxygate-nodes.txt"
$lines | Set-Content -Path $linesPath -Encoding UTF8

$csvPath = Join-Path $OutDir "proxygate-nodes.csv"
$nodes |
    Select-Object SlotId, Name, Host, Port,
        @{Name = "Username"; Expression = { $_.User } },
        @{Name = "Password"; Expression = { $_.Pass } },
        ExitIp, Checked, Status, Asn, AsnOrg |
    Export-Csv -Path $csvPath -NoTypeInformation -Encoding UTF8

Write-Host ""
Write-Host "tunnel: ssh pid $($tunnel.Id) -> $SshTarget (ports $($nodes[0].Port)-$($nodes[$nodes.Count - 1].Port))"
$nodes | ForEach-Object {
    Write-Host ("  slot {0,2}  {1,5}  {2,-16} {3,-10} {4}" -f $_.SlotId, $_.Port, $_.ExitIp, $_.Status, $_.Name)
}
Write-Host ""
Write-Host "files:"
Write-Host "  $clashPath"
Write-Host "  $providerPath"
Write-Host "  $singboxPath"
Write-Host "  $linesPath"
Write-Host "  $csvPath"
Write-Host ""
Write-Host "Clash/Mihomo: import proxygate-clash.yaml as a profile (mixed port 7890)."
Write-Host "sing-box:     run with proxygate-singbox.json (mixed port 7890)."
Write-Host "SOCKS5 apps:  use 127.0.0.1:<port>, username/password from proxygate-nodes.csv."
