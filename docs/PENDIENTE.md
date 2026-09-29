# Pendiente

Lo que falta, en el orden en que conviene hacerlo. Cada punto dice qué hay hoy, qué falta y cómo se verifica.

## 1. La prueba de punta a punta con videos reales

**Hoy está escrito y compila:**

- `internal/ffx`: encontrar ffmpeg/ffprobe, leer las propiedades del video (incluida la rotación y si es HDR), correr ffmpeg leyendo el progreso de `-progress`.
- `internal/squash`: el planificador (7 tests), la codificación en dos pasadas con corrección del tamaño por el método de la secante (se rehace solo la segunda pasada), la conversión HDR → SDR y la medición de calidad con VMAF sobre tres muestras.
- `internal/vidsquash` y `main.go`: la interfaz completa (flags, presets de servicios, progreso vivo, tarjeta final), probada sin ffmpeg ([VERIFICACION.md](VERIFICACION.md)).

**Falta correrla contra videos de verdad** (con `winget install Gyan.FFmpeg`):

- un clip sintético 1080p60 con audio (`ffmpeg -f lavfi -i testsrc2=s=1920x1080:r=60:d=20 -f lavfi -i sine=d=20 -shortest clip.mp4`);
- un video real de celular, vertical;
- un video HDR de iPhone (HLG) con rotación;
- objetivos de 10, 16 y 25 MB en cada uno, y un recorte con `--from`/`--to`.

En todos, `ffprobe` tiene que confirmar que el tamaño es menor o igual al objetivo y que la duración y las pistas se conservan; en el vertical y el rotado, que la orientación de la salida es la que se ve en el teléfono. Lo que conviene mirar en esa primera corrida, porque ningún test lo cubre:

- que la rotación se aplique una sola vez (ffmpeg rota solo al decodificar; si además se copian los metadatos de rotación, el video saldría girado dos veces);
- un video con carátula (una imagen adjunta como primera pista de video): `-map 0:v:0` podría tomar la imagen en vez del video;
- un video sin fps declarado o con fps variable;
- la cadena de tone mapping con un HDR real de iPhone (Dolby Vision 8.4, fps variables): con originales sintéticos ya está probada, con el ffmpeg de winget y seis versiones más ([VERIFICACION.md](VERIFICACION.md#hdr-tone-mapping-y-medición));
- que la corrección converja en uno o dos intentos, como dice el diseño.

Con esa corrida pasada, vidsquash llega a 1.0.0.

## 2. `--analyze`

Elegir resolución y fps midiendo (codificar muestras de cada candidata y quedarse con la de mejor VMAF) en vez de solo por bits por píxel. Está diseñado, no escrito.

## 3. Menores

- Un tamaño objetivo que falta es un error de uso y hoy sale con código 3; debería ser 2, como los demás errores de la línea de comandos.
- Cuando la salida va a otro disco que `%TEMP%`, el movimiento final reintenta el renombre cinco veces (~1,2 s) antes de copiar: se puede reconocer `ERROR_NOT_SAME_DEVICE` y copiar directo.
- Tests de `ffx` con un ffprobe falso (un exe de prueba que responde el JSON de casos grabados), para cubrir la lectura de rotación y HDR sin ffmpeg.

## Distribución

- Hecho: la CI ([`ci.yml`](../.github/workflows/ci.yml)) corre en cada push las pruebas y la frontera del `.exe` ([`frontera.ps1`](../.github/frontera.ps1)).
- Falta: con la prueba del punto 1, sumar a la CI el clip sintético (ffmpeg en el runner, como la CI de img), y recién ahí la release 1.0.0, con el `vidsquash.exe`, su SHA256 y el mismo esquema de etiqueta y reproducibilidad que las otras herramientas.
