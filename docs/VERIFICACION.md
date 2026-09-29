# Verificación

vidsquash está en construcción (0.1.0). Esta página dice qué está comprobado y qué no, sin redondear.

## Lo que está comprobado

### Tests de Go

```powershell
go test ./...
.\build.ps1 -Test     # go vet + go test y después compila
```

62 tests en `cli`, `ffx`, `fsx`, `textdist`, `tui`, `win/desk` y `squash`. Los 7 del planificador (`go test ./internal/squash/`) comprueban, sobre videos descritos (sin ffmpeg):

- el plan nunca promete más bytes que el objetivo: video + audio + contenedor ≤ objetivo;
- con pocos bits por píxel, primero se bajan los fps (60 → 30) y después la resolución;
- los videos verticales se escalan por el lado corto;
- nunca se agranda un video chico, y con bits de sobra se conserva todo;
- un objetivo imposible da `ErrTooSmall` con una sugerencia, no un plan roto;
- el recorte se respeta;
- las reglas del audio: con pocos bits pasa a mono y nunca supera al original.

De `ffx` se prueba que encuentra ffmpeg en el paquete que deja `winget install Gyan.FFmpeg` (un `LOCALAPPDATA` armado en una carpeta temporal); leer el video, codificar y medir se prueban con los tests de HDR de abajo, que necesitan ffmpeg.

Hay también un test de dependencias: `go list -deps` sobre `tui` no puede traer `net`, `net/netip` ni `os/exec` (vidsquash sí lanza procesos, pero eso vive en `ffx`).

### HDR: tone mapping y medición

Cinco tests. Los que corren ffmpeg generan sus originales (testsrc2 llevado a BT.2020 con PQ o con HLG, en ProRes de 10 bits dentro de un `.mov`, y uno vertical con rotación, como los graba un celular) y se saltean, diciéndolo, si ffmpeg no está o no trae zimg: en el runner de GitHub no corren.

- En toda cadena con tone mapping (codificar sin cambios, achicando, a la mitad de fps y la referencia de la medición), el `zscale` que aplica la matriz BT.709 va seguido de un `format=` fijo.
- Un original SDR no pasa por el tone mapping, y en la medición el tone mapping va solo del lado del original.
- Sin cambio de tamaño, la referencia de la medición es idéntica bit a bit (PSNR ∞) a lo que recibe el codificador: PQ, HLG y vertical rotado.
- Al achicar, lo que recibe el codificador está en BT.709, como dice la etiqueta del archivo: se compara contra el mismo original convertido sin achicar y reducido con zimg.
- De punta a punta (`ffx.Probe`, el plan, las dos pasadas y la medición), con el ffmpeg de winget:

| original | salida | SSIM | VMAF |
|---|---|---|---|
| PQ 640×360 | 640×360 · 570 KB | 0,9992 | 98,2 |
| HLG 640×360 | 426×240 · 75 KB | 0,9791 | 88,1 |
| HLG vertical rotado | 360×640 · 569 KB | 0,9992 | 98,1 |

**El bug que cierran.** El último `zscale` del tone mapping negociaba su formato de salida con el filtro siguiente. Si era un `scale` (al achicar, y siempre en la medición), zscale entregaba su RGB flotante de trabajo, el `m=bt709:r=tv` no se aplicaba y la conversión a YUV la hacía swscale: con BT.601 hasta ffmpeg 6.1, en un archivo marcado BT.709. La medición comparaba la salida contra otra conversión del original, distinta de la que había recibido el codificador. Ahora la cadena termina en `format=yuv420p` y la conversión la hace siempre zimg. Sin achicar, el codificador recibe exactamente los mismos cuadros que antes (framemd5 idéntico); al achicar un 4K HLG, contra la conversión ideal en punto flotante, pasa de 47,5 a 50,7 dB con ffmpeg 8.1 y de 29,2 a 47,4 dB con la 6.1, y el filtrado es ~10 % más rápido.

La matriz de versiones corre estos tests con cada ffmpeg:

```powershell
python internal\squash\_oracle\versiones.py <carpeta de ffmpeg> [<carpeta> ...]
```

| ffmpeg | referencia contra codificador, antes | después | achicado en BT.709, antes | después |
|---|---|---|---|---|
| 5.1.3 y 6.1.1 | 29,6 dB | ∞ | 29,6 dB (BT.601) | 54,4 dB |
| 7.0.2 | 40,6 dB | ∞ | 40,5 dB | 54,4 dB |
| 7.1.1, 8.0.1, 8.1.1 y 9.0.2 | 47 a 49 dB | ∞ | 45,0 dB | 54,4 dB |

Después del arreglo, las siete versiones dan lo mismo en todos los casos. La 5.1.3 (la de UiPath) saltea la prueba de punta a punta porque no trae libx264, y el caso vertical porque no tiene `-display_rotation` para escribir la rotación.

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

Todo lo que pasa después de encontrar ffmpeg, con videos de verdad. Leer el video, codificar, corregir el tamaño y medir la calidad ya corren de punta a punta en los tests, pero sobre originales sintéticos de 2 s: **nunca corrieron contra un video real.** La prueba que falta, con `ffprobe` como oráculo (independiente de vidsquash), está en [PENDIENTE.md](PENDIENTE.md): en cada caso, el tamaño final tiene que ser menor o igual al objetivo, y la duración y las pistas tienen que conservarse.

**Referencias de velocidad** medidas en la PC de desarrollo cuando se escribió el motor: x264 `medium` a 720p30 ≈ 1,5 veces el tiempo real; VMAF sobre 20 s de 1080p60 ≈ 22 s. Por eso la calidad se mide en muestras y no en el video entero.
