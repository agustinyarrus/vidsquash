<#
.SYNOPSIS
  La frontera de vidsquash.exe, sin codificar nada: la ayuda, la versión y
  cada validación de la línea de comandos, con su mensaje y su código de
  salida.

.DESCRIPTION
  Es lo que se puede comprobar del .exe sin un video: todo lo que pasa antes
  de llamar a ffmpeg. Corre en una carpeta temporal con un clip.mp4 de un
  byte (ninguna validación lo abre). El último caso, un pedido válido, tiene
  que decir "no encontré ffmpeg": solo corre si ffmpeg no está donde
  vidsquash lo busca (junto al .exe, en el PATH, en WinGet\Links o en el
  paquete Gyan.FFmpeg de WinGet\Packages); en el runner de GitHub no está.
  Si está, se saltea y lo dice.

.EXAMPLE
  .\build.ps1
  .\.github\frontera.ps1 -Exe .\dist\vidsquash.exe
#>
#Requires -Version 7
[CmdletBinding()]
param(
    [Parameter(Mandatory)] [string] $Exe
)
$ErrorActionPreference = 'Stop'
try { [Console]::OutputEncoding = [Text.UTF8Encoding]::new($false) } catch { }
$Exe = (Resolve-Path $Exe).Path
$version = [regex]::Match((Get-Content (Join-Path $PSScriptRoot '..\internal\version\version.go') -Raw), 'Version\s*=\s*"([^"]+)"').Groups[1].Value

$casos = @(
    @{ Args = @('--help'); Codigo = 0; Dice = 'comprime un video para que entre en un tamaño' }
    @{ Args = @('--version'); Codigo = 0; Dice = "vidsquash $version" }
    @{ Args = @('noexiste.mp4', '-s', '10MB'); Codigo = 3; Dice = 'noexiste.mp4 no existe' }
    @{ Args = @('clip.mp4'); Codigo = 3; Dice = 'falta el tamaño objetivo: -s 25MB o --for discord' }
    @{ Args = @('clip.mp4', '-s', '50KB'); Codigo = 3; Dice = '50,0 KB es demasiado poco para un video' }
    @{ Args = @('clip.mp4', '-s', '10MB', '--from', '1:00', '--to', '0:30'); Codigo = 3; Dice = '--to tiene que ser posterior a --from' }
    @{ Args = @('clip.mp4', '--for', 'discrod'); Codigo = 2; Dice = '¿quisiste decir "discord"?' }
    @{ Args = @('clip.mp4', '-s', '10MB', '--presset', 'slow'); Codigo = 2; Dice = '--preset' }
)
$ffmpeg = @(@(
    (Join-Path (Split-Path $Exe) 'ffmpeg.exe')
    (Get-Command ffmpeg -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1 -ExpandProperty Source)
    $(if ($env:LOCALAPPDATA) { Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Links\ffmpeg.exe' })
    $(if ($env:LOCALAPPDATA) { Get-ChildItem (Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Packages\Gyan.FFmpeg*\*\bin\ffmpeg.exe') -ErrorAction SilentlyContinue | Select-Object -Last 1 -ExpandProperty FullName })
) | Where-Object { $_ -and (Test-Path -LiteralPath $_) })
$sinFfmpeg = @{ Args = @('clip.mp4', '-s', '10MB'); Codigo = 3; Dice = 'no encontré ffmpeg (instalalo con: winget install Gyan.FFmpeg)' }
if (-not $ffmpeg) { $casos += $sinFfmpeg }

$dir = Join-Path ([IO.Path]::GetTempPath()) "vidsquash-frontera-$PID"
New-Item -ItemType Directory -Force $dir | Out-Null
[IO.File]::WriteAllBytes((Join-Path $dir 'clip.mp4'), [byte[]]@(0))
$env:NO_COLOR = '1'
$ok = 0
Push-Location $dir
try {
    foreach ($c in $casos) {
        $salida = (& $Exe @($c.Args) 2>&1 | Out-String)
        $codigo = $LASTEXITCODE
        $linea = "vidsquash $($c.Args -join ' ')"
        if ($codigo -eq $c.Codigo -and $salida.Contains($c.Dice)) {
            $ok++
            Write-Host "  ✓ $linea  →  $codigo · $($c.Dice)"
        } else {
            Write-Host "  ✗ $linea  →  $codigo (esperaba $($c.Codigo) y «$($c.Dice)»)"
            Write-Host ($salida.TrimEnd() -replace '(?m)^', '      ')
        }
    }
} finally {
    Pop-Location
    Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
}
if ($ffmpeg) { Write-Host "  - salteado: sin ffmpeg (hay uno en $($ffmpeg[0]))" }
Write-Host ''
Write-Host "  $ok de $($casos.Count) casos de la frontera con su mensaje y su código"
if ($ok -ne $casos.Count) { exit 1 }
exit 0
