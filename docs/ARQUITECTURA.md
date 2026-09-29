# Arquitectura

> vidsquash está en construcción (0.1.0): esto describe cómo está escrito, no algo que ya se haya comprobado con videos reales. Ver [PENDIENTE.md](PENDIENTE.md).

## Principios

- **Go puro, sin CGO y sin módulos externos.** vidsquash compila a un único `.exe`; el trabajo pesado lo hace ffmpeg, que corre como proceso aparte.
- **El plan es puro.** `squash.MakePlan` recibe lo que dijo ffprobe y las opciones, y devuelve la receta o por qué no se puede, sin tocar el disco ni lanzar nada. Por eso es lo que tiene tests.
- **Validar en la frontera.** Tamaño, tiempos, códec y salida se validan antes de buscar ffmpeg: un error de uso no espera a que arranque nada.
- **Nunca más grande que lo pedido.** Si ningún intento entra en el objetivo, es un error; no se entrega "casi".
- **Sin archivos a medias.** Los intentos se escriben en una carpeta de trabajo propia (en `%TEMP%`) que se borra al final; el mejor se mueve al destino: un renombre si está en el mismo disco, y si no, una copia a un temporal al lado del destino que después se renombra. Un corte o un Ctrl+C no deja nada a medio escribir en la carpeta del video.

## El recorrido de una corrida

1. `main.go` abre la consola (`tui.Open`), llama a `vidsquash.Main` con la versión y sale con el código que devuelve.
2. `validar`: que el video exista y no sea una carpeta; el objetivo (`-s` o `--for`, al menos 100 KB); `--from` y `--to`; la salida (por defecto `<nombre>-<tamaño>.mp4` al lado del original), que sea `.mp4`, que no sea la entrada y que no exista sin `--force`.
3. `ffx.Locate` busca ffmpeg y ffprobe: junto al exe, en el `PATH` y donde los deja winget (`WinGet\Links` o el paquete de Gyan.FFmpeg en `WinGet\Packages`).
4. `ffx.Probe` lee duración, tamaño, la pista de video (dimensiones, fps, rotación, si es HDR) y la de audio.
5. `squash.MakePlan` arma la receta y la herramienta la muestra: origen, objetivo (bits de video + audio) y receta (resolución, fps, códec, preset), con el porqué de cada decisión.
6. `squash.Encode` corre las pasadas con una barra viva por fase.
7. `squash.Measure` mide la calidad (salvo `--no-quality`), y la tarjeta final resume tamaño, intentos y calidad.

## ffx

- Busca ffmpeg junto al exe (instalación portable), después en el `PATH`, después en los accesos de winget (`WinGet\Links`) y en el paquete de Gyan.FFmpeg (`WinGet\Packages\Gyan.FFmpeg*\*\bin`). Ese último es el caso de `winget install Gyan.FFmpeg`: agrega el bin del paquete al `PATH` del usuario, que una terminal abierta antes de instalar no ve, y deja Links vacío.
- Corre ffmpeg y ffprobe con cancelación: el contexto de Ctrl+C mata el proceso y no deja basura.
- Lee el progreso de `-progress pipe:1` (tiempo de salida, velocidad, cuadros) y se queda con el final de stderr para explicar un fallo.
- No abre ventanas propias (`CREATE_NO_WINDOW` en Windows).

## squash: el planificador

- **Presupuesto**: el objetivo menos un margen del 2 %, menos el overhead estimado del MP4: 4.096 B fijos, 12 B por cuadro de video y 6 B por cuadro de audio AAC (1.024 muestras a 48 kHz). Lo que queda, dividido por la duración (la del tramo si hay recorte), son los kb/s totales.
- **Audio**: por escalones según los kb/s totales (160, 128, 96, 64, 48 o 32 kb/s), nunca por encima del original, a estéreo como mucho (un 5.1 baja a estéreo) y a mono por debajo de 64 kb/s: un mono limpio suena mejor que un estéreo estrujado.
- **Resolución y fps**:
  - La escalera se arma por el **lado corto** (2160, 1440, 1080, 720, 540, 480, 360, 240, empezando por el original o por `--max-res`), para que un video vertical no se trate como uno de 1920 de alto. Nunca agranda.
  - Se elige la primera combinación de resolución y fps cuyos bits por píxel alcanzan: 0,050 para H.264, 0,035 para H.265.
  - A igual resolución prueba primero los fps originales y después la mitad (solo si el original pasa de 30 y la mitad no baja de 24), con un 10 % de tolerancia documentado a favor de conservar la resolución. `--keep-fps` saca la mitad de la lista.
  - Si ni la última alcanza, se usa igual y se avisa que la imagen va a verse gruesa.
  - Las dimensiones se redondean a par (lo exige yuv420p).
- **Imposibles**: por debajo de 45 kb/s de video ya no es video, es un mosaico; el plan devuelve un error con una sugerencia (recortar o pedir más).

## squash: la codificación

- **Dos pasadas**: la primera estudia el video entero y anota dónde hacen falta bits; la segunda reparte el presupuesto con esa información.
- **Corrección**: si el archivo se pasa del objetivo o queda por debajo del 90 %, se repite solo la segunda pasada, porque las estadísticas de la primera sirven para cualquier bitrate. El próximo bitrate sale del método de la secante sobre tamaño(bitrate), que es monótona: con un intento, proporción directa sobre la parte de video; con dos, la secante entre los dos últimos. Apunta al 98,5 % del objetivo, y la última corrección al 97 %, para que entre sí o sí. Como mucho tres correcciones. Con salvaguardas para cuando la curva se aplana (al codificador le sobran bits): se detiene si subir el bitrate casi no agrandó el archivo, no sale del intervalo entre un intento que entra y otro que se pasa, sin intervalo a lo sumo duplica o divide por dos el bitrate, y no repite un bitrate ya probado.
- Se queda con el intento **más grande que entra** en el objetivo.
- **Filtros**: tone mapping si es HDR, `scale` con lanczos si cambia la resolución, `fps` si cambian los cuadros, y siempre `format=yuv420p`, el que reproduce cualquier cosa.
- **HDR → SDR**: PQ o HLG a BT.709 con la curva Hable, pasando por luz lineal en punto flotante (`zscale`, de zimg). La cadena termina en `format=yuv420p`, que obliga a zimg a hacer la conversión a YUV: sin él, un `scale` a continuación recibía el RGB flotante y la matriz la elegía swscale (BT.601 hasta ffmpeg 6.1, en un archivo marcado BT.709).
- **Contenedor**: `-movflags +faststart` (el índice va al principio y el video arranca antes de bajarse entero, lo que hace falta en un chat) y los metadatos del original. En H.265 va la etiqueta `hvc1`: sin ella, iPhone y Mac no lo reproducen.
- `--from` va antes de `-i`: al transcodificar, la búsqueda es rápida y exacta.

## squash: la calidad

- **VMAF** (la métrica perceptual de Netflix, 0–100) sobre tres ventanas de 2 s, al 20, 50 y 80 % del video; un video de 8 s o menos se mide entero. Medir el archivo completo costaría otra pasada.
- Referencia y salida se llevan a la misma resolución (como mucho 1080p, para la que está calibrado el modelo) y a los mismos fps; si hubo tone mapping, la referencia pasa por la misma cadena que la codificación, para comparar SDR contra los mismos cuadros SDR que recibió el codificador.
- Si el ffmpeg instalado no trae libvmaf, se usa SSIM. La tarjeta traduce el número a palabras con los umbrales usuales ("indistinguible del original" desde 93, "muy buena" desde 85…).

## El núcleo de consola

### tui

- **Región viva**: el progreso de cada fase se redibuja en el lugar a 20 cuadros por segundo y las líneas permanentes se imprimen por encima, con un orden de candados fijo para que no haya deadlock.
- **Barra** con resolución de 1/8 de columna; en Windows Terminal el avance también se publica en el ícono y la pestaña (OSC 9;4).
- **Tarjeta** sin bordes y **formato es-AR**: miles con punto, decimales con coma, bytes en unidades decimales, que son las que usan los servicios para sus límites.
- Texto plano sin escapes cuando la salida no es una consola.
- **Renglones que no entran**: los errores (`✗`) se parten en palabras al ancho de la ventana, con las líneas de más alineadas después de la marca (`Term.Marked`); a un pipe van enteros. Antes la consola los cortaba donde caían.

### cli

- Flags al estilo GNU; binders tipados con validación: enteros acotados, enumerados (`--for`, `--codec`, `--preset`), **tamaños** (`25MB`, `8M`, `1,5GB`, `500KiB`, decimales y binarios) y **tiempos** (`90`, `1:30`, `01:02:03.5`, `1m30s`).
- Un flag o un valor mal escrito sugiere el más parecido por distancia de Damerau–Levenshtein (`textdist`).
- `Interrupt`: el primer Ctrl+C cancela el contexto (ffmpeg se corta y se limpian los temporales); el segundo sale en seco.

### fsx y win/desk

- `fsx`: nombres para mostrar y comprobaciones de archivos (`SameFile`, `Exists`).
- `win/desk`: el modo y el tamaño de la consola por `syscall`. Solo carga `kernel32.dll`, una KnownDLL que Windows toma siempre de System32 (con otra DLL nombrada sin ruta, `LoadLibrary` buscaría primero junto al exe).

### version

`Version` y `Commit` son variables que `build.ps1` pisa con `-ldflags -X`: `vidsquash --version` dice `0.1.0+abc1234`. Compilado con `go install`, dice `0.1.0`.
