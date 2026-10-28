#Requires -Version 5
<#
.SYNOPSIS
  Lists PatchAssistant2J as an "Open with" candidate for .jar files.

.DESCRIPTION
  Wails produces a single .exe and writes nothing to the registry, so a
  double-clicked .jar will never pick this program on its own. The original
  Java build shipped a GluonFX installer (.msi) that registered the
  association for you; a portable build has no installer, so the registry
  keys have to be written once, by hand.

  Two keys are written, and BOTH are needed:

  1. HKCU\Software\Classes\PatchAssistant2J.JAR -- our own ProgId, listed under
     .jar\OpenWithProgids. Command: `<exe> "--target=%1"`.
  2. HKCU\Software\Classes\Applications\PatchAssistant2J.exe\shell\open\command
     -- the ProgId Windows' "Open with" UI looks up for an exe by its file
     name. The Java and Go builds share the file name PatchAssistant2J.exe,
     so this key may already exist pointing at the Java build with a bare
     `%1`; the Go app parses that (unlike the original), but pointing BOTH
     names at the Go build removes the trap where the list shows two
     identically named entries and the wrong one silently opens the Java
     build, which ignores a bare path.

  The previous value of key 2 is saved to
  PatchAssistant2J.exe.assoc-backup.txt next to this script so that
  unregister-jar-association.ps1 can put it back. Per-user only, no admin
  rights needed.

  What this can NOT do: Windows 10/11 protects the per-user default handler
  (UserChoice) with a hash. No program may claim "default" silently. After
  running this, pick "PatchAssistant2J" once in
    Right-click a .jar > Open with > Choose another app
  and tick "Always use this app". That last click is yours to make.

  Run unregister-jar-association.ps1 to undo.

.NOTES
  Verified against PowerShell 5.1 on the project machine: the registry keys
  and the exact command line were read back after a run.

  Background on the two keys: right-clicking a .jar > Open with resolves an
  entry by exe file name through HKCR\Applications, not through
  OpenWithProgids. If the Applications key still points at the Java build
  (which only understands `--key=value`, so a bare `%1` starts the UI without
  loading the file), the user gets exactly the symptom "the window opened but
  my jar is not in it".
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$ProgId = 'PatchAssistant2J.JAR'
$AppProgId = 'Applications\PatchAssistant2J.exe'

# Script sitting next to the exe, or at the project root (patch-assistant-go).
function Get-ScriptDir {
    if ($PSScriptRoot) { return $PSScriptRoot }
    if ($MyInvocation.MyCommand.Path) { return Split-Path -Parent $MyInvocation.MyCommand.Path }
    return (Get-Location).Path
}

$scriptDir = Get-ScriptDir
$BackupFile = Join-Path $scriptDir 'PatchAssistant2J.exe.assoc-backup.txt'

# Script sitting next to the exe, or at the project root (patch-assistant-go).
function Get-ExePath {
    foreach ($rel in @('PatchAssistant2J.exe', 'gui\build\bin\PatchAssistant2J.exe')) {
        $p = Join-Path $scriptDir $rel
        if (Test-Path -LiteralPath $p) { return $p }
    }
    return $null
}

$exe = Get-ExePath
if (-not $exe) {
    Write-Error 'PatchAssistant2J.exe not found. Put this script next to PatchAssistant2J.exe, or run it from the project root (patch-assistant-go).'
    exit 1
}

Write-Host 'Using executable:'
Write-Host "  $exe"
Write-Host ''

# --- Key 2 first: back up whatever is there before touching it ------------
$appOpen = "HKCU:\Software\Classes\$AppProgId\shell\open\command"
if (-not (Test-Path -LiteralPath $BackupFile)) {
    $orig = $null
    if (Test-Path $appOpen) { $orig = (Get-ItemProperty -Path $appOpen).'(default)' }
    # UTF-8 **without BOM**: the backup round-trips paths verbatim. -Encoding
    # ASCII would turn any non-ASCII character (this project lives under
    # "D:\Download\<Chinese>...") into "?", silently corrupting the restore.
    if ($orig) {
        [System.IO.File]::WriteAllText($BackupFile, $orig, (New-Object System.Text.UTF8Encoding($false)))
        Write-Host 'Previous "Open with" command saved to:'
        Write-Host "  $BackupFile"
        Write-Host "  $orig"
    }
    else {
        [System.IO.File]::WriteAllText($BackupFile, '(none)', (New-Object System.Text.UTF8Encoding($false)))
        Write-Host 'No previous Applications\PatchAssistant2J.exe registration;'
        Write-Host 'unregister will remove the key created here.'
    }
    Write-Host ''
}

$command = ('"{0}" "--target=%1"' -f $exe)

$base = "HKCU:\Software\Classes\$ProgId"
$open = Join-Path $base 'shell\open\command'

New-Item -Path $open -Force | Out-Null
Set-ItemProperty -Path $base -Name '(default)' -Value 'PatchAssistant2J'
Set-ItemProperty -Path $open -Name '(default)' -Value $command

New-Item -Path $appOpen -Force | Out-Null
Set-ItemProperty -Path $appOpen -Name '(default)' -Value $command

$openWith = 'HKCU:\Software\Classes\.jar\OpenWithProgids'
New-Item -Path $openWith -Force | Out-Null
Set-ItemProperty -Path $openWith -Name $ProgId -Value ''

Write-Host "Registered both ProgIds for .jar ""Open with"":"
Write-Host "  $base"
Write-Host "  HKCU:\Software\Classes\$AppProgId"
Write-Host ''
Write-Host 'Next step: right-click any .jar, choose Open with > Choose another app,'
Write-Host 'pick PatchAssistant2J, and tick "Always use this app".'
