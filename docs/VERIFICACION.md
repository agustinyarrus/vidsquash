# Verificación

vidsquash está en construcción (0.1.0). Esta página dice qué está comprobado y qué no, sin redondear.

## Lo que está comprobado

### Tests de Go

```powershell
go test ./...
.\build.ps1 -Test     # go vet + go test y después compila
```

57 tests en `cli`, `ffx`, `fsx`, `textdist`, `tui`, `win/desk` y `squash`. Los 7 del planificador (`go test ./internal/squash/`) comprueban, sobre videos descritos (sin ffmpeg):

- el plan nunca promete más bytes que el objetivo: video + audio + contenedor ≤ objetivo;
- con pocos bits por píxel, primero se bajan los fps (60 → 30) y después la resolución;
- los videos verticales se escalan por el lado corto;
- nunca se agranda un video chico, y con bits de sobra se conserva todo;
- un objetivo imposible da `ErrTooSmall` con una sugerencia, no un plan roto;
- el recorte se respeta;
- las reglas del audio: con pocos bits pasa a mono y nunca supera al original.

De `ffx` se prueba solo que encuentra ffmpeg en el paquete que deja `winget install Gyan.FFmpeg` (un `LOCALAPPDATA` armado en una carpeta temporal); leer el video y la codificación (`squash/encode.go`, `squash/quality.go`) no tienen tests: dependen de ffmpeg.

Hay también un test de dependencias: `go list -deps` sobre `tui` no puede traer `net`, `net/netip` ni `os/exec` (vidsquash sí lanza procesos, pero eso vive en `ffx`).

### La frontera del exe, en cada corrida de la CI

[`.github/frontera.ps1`](../.github/frontera.ps1) corre el `.exe` en una carpeta temporal con un `clip.mp4` de un byte (ninguna validación lo abre) y comprueba el mensaje y el código de cada caso de la tabla de abajo, más un flag mal escrito (`--presset`, código 2). El de "no encontré ffmpeg" solo corre si ffmpeg no está donde vidsquash lo busca (en el runner de GitHub no está); si está, se saltea y lo dice. La CI lo corre en cada push, después de `build.ps1` ([`ci.yml`](../.github/workflows/ci.yml)).

La CI no corrió todavía en GitHub (el repo no se publicó): se validó con actionlint y se simuló en la PC de desarrollo, con un clon limpio, Go 1.26.0 y cachés vacías. Todos los pasos en verde. La frontera, 9 de 9 con un `LOCALAPPDATA` vacío (como el runner, sin ffmpeg a la vista); en la PC, que tiene el ffmpeg de winget, 8 de 8 con el caso de ffmpeg salteado y dicho.

### El exe, en una máquina sin ffmpeg (28/09/2026)

Con el `vidsquash.exe` que compila este repo, en Windows 11 con Go 1.26.4:

| Qué se corrió | Qué dio |
|---|---|
| `vidsquash --help`, `vidsquash --version` | la ayuda completa y `vidsquash 0.1.0`, código 0 |
| un archivo que no existe | "noexiste.mp4 no existe", código 3 |
| un archivo sin `-s` ni `--for` | "falta el tamaño objetivo: -s 25MB o --for discord", código 3 |
| `-s 50KB` | "50,0 KB es demasiado poco para un video", código 3 |
| `--from 1:00 --to 0:30` | "--to tiene que ser posterior a --from", código 3 |
| `--for discrod` | sugiere "discord", código 2 |
| un archivo válido sin ffmpeg instalado | "no encontré ffmpeg (instalalo con: winget install Gyan.FFmpeg)", código 3 |

Esa primera corrida del exe encontró un mensaje crudo: un archivo inexistente daba el error de Windows en inglés (`GetFileAttributesEx … The system cannot find the file specified.`); ahora dice "no existe", como las demás validaciones.

## Lo que falta comprobar

Todo lo que pasa después de encontrar ffmpeg: leer el video, codificar, corregir el tamaño y medir la calidad. **Nunca corrió contra un video real.** La prueba que falta, con `ffprobe` como oráculo (independiente de vidsquash), está en [PENDIENTE.md](PENDIENTE.md): en cada caso, el tamaño final tiene que ser menor o igual al objetivo, y la duración y las pistas tienen que conservarse.

**Referencias de velocidad** medidas en la PC de desarrollo cuando se escribió el motor: x264 `medium` a 720p30 ≈ 1,5 veces el tiempo real; VMAF sobre 20 s de 1080p60 ≈ 22 s. Por eso la calidad se mide en muestras y no en el video entero.
