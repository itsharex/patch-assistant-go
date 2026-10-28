#Requires -Version 5
<#
  DiagJarAssoc.ps1

  Dump the .jar file-association state so we can see why double-clicking a
  .jar does not load it into PatchAssistant2J while dragging it in does.

  It prints:
    - The per-user default handler (UserChoice.ProgId) for .jar
    - The .jar class default + OpenWithProgids list
    - The open command for the default ProgId (this is what double-click runs)
    - The open command for Applications\PatchAssistant2J.exe and
      PatchAssistant2J.JAR (the two keys register-jar-association.ps1 writes)
    - The exe path each command points at, and whether that exe actually exists
      (so we can tell the Go build from a leftover Java build)

  Output is written to jar-assoc-diag.txt next to this script (UTF-8, no BOM)
  and also echoed. Open the .txt and paste it back.
#>
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'

function Get-Reg {
    param($Path, $Name = '(default)')
    try {
        if (Test-Path -LiteralPath $Path) {
            $v = (Get-ItemProperty -LiteralPath $Path -ErrorAction Stop).$Name
            if ($null -eq $v) { return '' }
            return [string]$v
        }
    } catch { }
    return $null
}

$out = [System.Collections.Generic.List[string]]::new()
function Add($s) { $out.Add($s) }

Add('=== .jar UserChoice (per-user default handler) ===')
$uc = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\.jar\UserChoice'
Add('ProgId : ' + (Get-Reg $uc 'ProgId'))
Add('Hash  : ' + (Get-Reg $uc 'Hash'))

Add('')
Add('=== .jar class default + OpenWithProgids ===')
Add('HKCU:\Software\Classes\.jar default : ' + (Get-Reg 'HKCU:\Software\Classes\.jar'))
$owp = 'HKCU:\Software\Classes\.jar\OpenWithProgids'
if (Test-Path -LiteralPath $owp) {
    $props = Get-ItemProperty -LiteralPath $owp -ErrorAction Stop
    foreach ($p in $props.PSObject.Properties) {
        if ($p.Name -eq 'PSPath' -or $p.Name -eq 'PSParentPath' -or
            $p.Name -eq 'PSChildName' -or $p.Name -eq 'PSDrive' -or
            $p.Name -eq 'PSProvider') { continue }
        Add('  OpenWithProgids.' + $p.Name + ' = ' + [string]$p.Value)
    }
}

function Report-Command($label, $progId) {
    Add('')
    Add('=== open command for: ' + $label + ' (' + $progId + ') ===')
    if ($null -eq $progId -or $progId -eq '') { Add('  (no ProgId)'); return }
    $cmd = Get-Reg ('HKCU:\Software\Classes\' + $progId + '\shell\open\command')
    Add('  command : ' + $cmd)
    if ($null -ne $cmd -and $cmd -ne '') {
        $m = [regex]::Match($cmd, '^"([^"]+)"|^(\S+)')
        $exe = if ($m.Success) {
            if ($m.Groups[1].Success) { $m.Groups[1].Value } else { $m.Groups[2].Value }
        } else { '' }
        Add('  exe     : ' + $exe)
        if ($exe -ne '' -and (Test-Path -LiteralPath $exe)) {
            $f = Get-Item -LiteralPath $exe
            Add('  exists  : yes  size=' + $f.Length + '  modified=' + $f.LastWriteTime)
        } else {
            Add('  exists  : NO')
        }
    }
}

Report-Command 'UserChoice default' (Get-Reg $uc 'ProgId')
Report-Command 'Applications\PatchAssistant2J.exe' 'Applications\PatchAssistant2J.exe'
Report-Command 'PatchAssistant2J.JAR' 'PatchAssistant2J.JAR'

Add('')
Add('=== expected Go build location ===')
$scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { (Get-Location).Path }
$goBuild = Join-Path $scriptDir 'gui\build\bin\PatchAssistant2J.exe'
Add('  ' + $goBuild)
if (Test-Path -LiteralPath $goBuild) {
    $f = Get-Item -LiteralPath $goBuild
    Add('  exists  : yes  size=' + $f.Length + '  modified=' + $f.LastWriteTime)
} else {
    Add('  exists  : NO')
}

$outPath = Join-Path $scriptDir 'jar-assoc-diag.txt'
[System.IO.File]::WriteAllText($outPath, ($out -join "`r`n"), (New-Object System.Text.UTF8Encoding($false)))
Add('')
Add('Wrote ' + $outPath)
Add('--- also echoed below ---')
$out | ForEach-Object { Write-Output $_ }
