#Requires -Version 5
<#
.SYNOPSIS
  Undoes register-jar-association.ps1.

.DESCRIPTION
  Removes our ProgId (PatchAssistant2J.JAR) and its listing under
  .jar\OpenWithProgids, and restores whatever
  Applications\PatchAssistant2J.exe\shell\open\command held before the first
  registration (saved in PatchAssistant2J.exe.assoc-backup.txt next to this
  script). Without that file the Applications key is left untouched, because
  there is no way to tell what it used to point at.

  Your current default handler (UserChoice) is not touched -- if you had
  already ticked "Always use this app" for PatchAssistant2J, change it back
  from  Open with > Choose another app.
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$ProgId = 'PatchAssistant2J.JAR'
$AppProgId = 'Applications\PatchAssistant2J.exe'
$AppBase = "HKCU:\Software\Classes\$AppProgId"
$AppOpen = "$AppBase\shell\open\command"

function Get-ScriptDir {
    if ($PSScriptRoot) { return $PSScriptRoot }
    if ($MyInvocation.MyCommand.Path) { return Split-Path -Parent $MyInvocation.MyCommand.Path }
    return (Get-Location).Path
}

$scriptDir = Get-ScriptDir
$BackupFile = Join-Path $scriptDir 'PatchAssistant2J.exe.assoc-backup.txt'

Remove-Item -Path "HKCU:\Software\Classes\$ProgId" -Recurse -Force -ErrorAction SilentlyContinue
Remove-ItemProperty -Path 'HKCU:\Software\Classes\.jar\OpenWithProgids' -Name $ProgId -Force -ErrorAction SilentlyContinue

if (Test-Path -LiteralPath $BackupFile) {
    $orig = [System.IO.File]::ReadAllText($BackupFile).Trim()
    if ($orig -eq '(none)') {
        Remove-Item -Path $AppBase -Recurse -Force -ErrorAction SilentlyContinue
        Write-Host 'Removed the Applications\PatchAssistant2J.exe registration created by the register script.'
    }
    elseif ($orig) {
        New-Item -Path $AppOpen -Force | Out-Null
        Set-ItemProperty -Path $AppOpen -Name '(default)' -Value $orig
        Write-Host 'Restored the previous Applications\PatchAssistant2J.exe command:'
        Write-Host "  $orig"
    }
    Remove-Item -LiteralPath $BackupFile -Force
}
else {
    Write-Host "No backup file found ($BackupFile)."
    Write-Host "Leaving $AppBase untouched -- its previous command is unknown."
}

Write-Host 'Removed PatchAssistant2J from the .jar "Open with" list.'
