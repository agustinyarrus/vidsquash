# vidsquash

Comprime un video para que entre en un tamaño máximo (los 10 MB de Discord, los 16 de WhatsApp, los 25 de un mail) con la mejor calidad posible, desde la consola de Windows. No pide un bitrate ni una calidad: pide el tamaño al que tiene que llegar y decide el resto.

```powershell
vidsquash partida.mp4 --for discord                    # 10 MB
vidsquash viaje.mov -s 16MB                            # para WhatsApp
vidsquash charla.mp4 -s 25MB --from 2:10 --to 5:40     # solo un tramo
vidsquash juego.mp4 -s 50MB --keep-fps -p slow         # conservar los 60 fps y exprimir calidad
```

> **Estado: 0.1.0, en construcción.** Compila, la interfaz está completa y el planificador tiene sus tests, pero **todavía no se probó de punta a punta con videos reales**: ni un clip sintético, ni uno de celular vertical, ni un HDR de iPhone. Hasta que eso pase (el plan está en [docs/PENDIENTE.md](docs/PENDIENTE.md)), tomalo como una versión para probar, no para confiarle el único original de un video.

## Qué hace (y qué se supone que hace)

- **Reparte los bits**: el objetivo menos un margen del 2 % y menos lo que ocupa el contenedor MP4 (estimado por cuadro de video y de audio) es el presupuesto. El audio baja de calidad antes que el video, pasa a mono por debajo de 64 kb/s y nunca supera al original.
- **Elige resolución y cuadros por segundo** por bits por píxel: la primera combinación que alcanza 0,050 en H.264 (0,035 en H.265). La escalera va por el **lado corto**, así un video vertical de celular no se trata como uno de 1920 de alto. Nunca agranda.
- **Dos pasadas con corrección**: la primera estudia el video entero; si la segunda se pasa del objetivo o queda por debajo del 90 %, se repite solo la segunda (las estadísticas de la primera sirven para cualquier bitrate), corrigiendo con el método de la secante sobre tamaño(bitrate). Se queda con el intento más grande que entra: **nunca entrega un archivo más grande que el pedido**.
- **HDR → SDR**: un video HDR (como los de iPhone) se convierte con tone mapping para que no se vea lavado en pantallas comunes.
- **Mide la calidad** al final con VMAF (0–100) sobre tres ventanas de 2 s contra el original, o con SSIM si el ffmpeg instalado no trae libvmaf.
- **Recorte** con `--from` y `--to` (`90`, `1:30`, `1m30s`), presets de servicios con `--for`, `--keep-fps`, `--max-res`, `--no-audio`, códec y preset de x264/x265.
- **Todo local**: el video no sale de la máquina. Un Ctrl+C corta ffmpeg y no deja archivos a medias.

## Instalación

vidsquash usa **ffmpeg** para codificar: `winget install Gyan.FFmpeg`. Lo busca junto al exe, después en el `PATH` y después donde lo deja winget.

Con [Go](https://go.dev/dl) 1.24 o más nuevo (el `go.mod` pide 1.26 y Go descarga sola esa versión la primera vez):

```powershell
go install github.com/agustinyarrus/vidsquash@latest
```

O desde el código, con la versión y el commit estampados en el exe:

```powershell
git clone https://github.com/agustinyarrus/vidsquash
cd vidsquash
.\build.ps1          # compila a dist\vidsquash.exe
.\build.ps1 -Test    # antes corre go vet y todos los tests
```

No tiene módulos externos de Go: solo la biblioteca estándar.

## Uso

`vidsquash --help` (salida real, sin colores):

```
  vidsquash  ·  comprime un video para que entre en un tamaño                                0.1.0

  uso
    vidsquash <video> -s <tamaño> [opciones]
    vidsquash clip.mp4 -s 25MB
    vidsquash clip.mp4 --for discord

  objetivo
    -s, --size TAMAÑO                tamaño máximo del archivo final: 25MB, 8M, 1,5GB, 500KiB
        --for discord|whatsapp|outlook|gmail
                                     límite de un servicio: discord 10 MB · whatsapp 16 MB ·
                                     outlook 20 MB · gmail 25 MB

  recorte
        --from TIEMPO                empezar en este punto (90, 1:30, 1m30s)
        --to TIEMPO                  terminar en este punto

  receta
    -c, --codec h264|h265            h264 se reproduce en todos lados; h265 ocupa ~30 % menos pero
                                     no todos lo abren (h264)
    -p, --preset veryfast|faster|fast|medium|slow|slower
                                     esfuerzo del codificador: más lento = mejor calidad al mismo
                                     tamaño (medium)
        --max-res N                  tope del lado corto (720 = como mucho 720p) (0)
        --keep-fps                   no bajar los cuadros por segundo (juegos, deportes)
        --no-audio                   descartar el audio (todos los bits al video)

  salida
    -o, --out ARCHIVO                archivo de salida (por defecto: <nombre>-<tamaño>.mp4 al lado
                                     del original)
    -f, --force                      sobrescribir la salida si ya existe
        --no-quality                 no medir la calidad al final (ahorra unos segundos)

  general
    -h, --help                       muestra esta ayuda
    -V, --version                    muestra la versión
        --no-color                   salida sin colores (también respeta NO_COLOR)
```

Los tamaños van en unidades decimales (1 MB = 1.000.000 bytes), como los anuncian los servicios; también se aceptan `MiB`/`GiB` y la coma decimal (`1,5GB`). Flags al estilo GNU; un flag o un valor mal escrito sugiere el más parecido (`--for discrod` → "¿quisiste decir "discord"?").

Códigos de salida: `0` todo bien · `2` línea de comandos inválida · `3` no se pudo (el archivo no existe, falta el tamaño, no hay ffmpeg, el objetivo no alcanza) · `130` cancelado con Ctrl+C.

## Cómo se verifica

Lo que hay hoy:

- **54 tests de Go** (`go test ./...`), 7 de ellos del planificador: el plan nunca promete más bytes que el objetivo, los videos verticales se escalan por el lado corto, nunca se agranda un video, con pocos bits primero bajan los fps, un objetivo imposible da un error con sugerencia y el audio sigue sus reglas.
- **El exe, sin ffmpeg**: la ayuda, `--version` y cada validación de la frontera (archivo que no existe, tamaño faltante o demasiado chico, `--to` antes de `--from`, un `--for` mal escrito, ffmpeg ausente) dan el mensaje y el código esperados.

Lo que falta es lo importante: correrlo contra videos de verdad y comprobar con `ffprobe` que el tamaño queda por debajo del objetivo y que la duración y las pistas se conservan. El detalle: [docs/VERIFICACION.md](docs/VERIFICACION.md).

## Cómo está hecho

```
main.go              el punto de entrada: abre la consola, llama a vidsquash.Main y sale con su código
internal/
  vidsquash/         la herramienta: flags, presets, la receta, el progreso en vivo y la tarjeta final
  squash/            el planificador (puro), las dos pasadas con corrección y la medición de calidad
  ffx/               encontrar y correr ffmpeg/ffprobe, leer el video y el progreso de -progress
  fsx/               escritura atómica, nombres para mostrar
  cli/               flags estilo GNU, tamaños y tiempos, ayuda, sugerencias
  tui/               consola: paleta, progreso vivo, tarjetas, formato es-AR
  textdist/          distancia de Damerau–Levenshtein
  win/desk/          modo y tamaño de la consola por syscall
  version/           versión y commit, estampados por build.ps1
```

La arquitectura y las decisiones del planificador: [docs/ARQUITECTURA.md](docs/ARQUITECTURA.md). Lo que falta, en orden: [docs/PENDIENTE.md](docs/PENDIENTE.md).

## Origen

vidsquash nació dentro de navaja, una suite de herramientas de consola para Windows que compartían un núcleo (la consola, los flags, los archivos). Ahí quedó con el motor escrito y sin ejecutable; como proyecto propio arranca en la 0.1.0, con su punto de entrada y la historia de git de sus archivos.

## Licencia

[MIT](LICENSE).
