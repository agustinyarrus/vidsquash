# Corre los tests HDR de vidsquash contra varias compilaciones de ffmpeg, para
# saber si el tone mapping y la medición dan lo mismo en todas.
#
#   python internal\squash\_oracle\versiones.py <carpeta de ffmpeg> [<carpeta> ...]
#
# Cada carpeta (la que tiene ffmpeg.exe y ffprobe.exe) se antepone al PATH, que
# es donde las busca ffx.Locate, y se corre go test -json con los tests HDR de
# internal/squash. La tabla muestra, por versión, el resultado de cada caso y el
# número que el test deja en su log: PSNR en dB (∞ = idéntico bit a bit) o el
# SSIM de la prueba de punta a punta.
#
# Compilaciones para probar: https://github.com/GyanD/codexffmpeg/releases (las
# "essentials" traen libzimg y libvmaf). Sale con 1 si algún caso falla.
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

REPO = Path(__file__).resolve().parents[3]
TESTS = [
    ("el tone mapping fija el formato", "TestTonemapFijaLaConversion"),
    ("referencia = codificador · PQ", "TestReferenciaHDRIgualAlCodificador/pq"),
    ("referencia = codificador · HLG", "TestReferenciaHDRIgualAlCodificador/hlg"),
    ("referencia = codificador · vertical", "TestReferenciaHDRIgualAlCodificador/hlg-vertical-rotado"),
    ("achicado en BT.709", "TestHDRAchicadoEnBT709"),
    ("punta a punta · PQ", "TestMedicionHDRDePuntaAPunta/pq-sin-achicar"),
    ("punta a punta · HLG achicado", "TestMedicionHDRDePuntaAPunta/hlg-achicado"),
    ("punta a punta · vertical", "TestMedicionHDRDePuntaAPunta/hlg-vertical-de-celular"),
]
RAICES = sorted({t.split("/")[0] for _, t in TESTS})
MEDIDA = re.compile(r"(PSNR) (\S+) dB|(SSIM) (\S+)")
VERSION = re.compile(r"ffmpeg version \D*(\d+(?:\.\d+)+)")

# La paleta de internal/tui: acentos Catppuccin claros sobre negro puro.
SAGE, ROSE, CREAM = "\x1b[38;2;181;223;168m", "\x1b[38;2;242;167;184m", "\x1b[38;2;238;223;184m"
LAVENDER, TEXT, SUBTLE, RESET = "\x1b[38;2;196;181;253m", "\x1b[38;2;205;214;244m", "\x1b[38;2;138;143;168m", "\x1b[0m"
ANCHO_ETIQUETA, ANCHO_CELDA = 38, 14


def pintar(color, texto):
    return f"{color}{texto}{RESET}" if sys.stdout.isatty() else texto


def version_de(carpeta: Path) -> str:
    """La versión que dice `ffmpeg -version` (5.1.3, 8.1.1…)."""
    out = subprocess.run([str(carpeta / "ffmpeg"), "-hide_banner", "-version"],
                         capture_output=True, text=True, encoding="utf-8", timeout=30).stdout
    m = VERSION.search(out)
    return m.group(1) if m else carpeta.name


def correr(carpeta: Path) -> dict[str, tuple[str, str]]:
    """Una corrida de go test con ese ffmpeg primero en el PATH.

    Devuelve test → (acción, medida). O(eventos): una pasada sobre el JSON.
    """
    env = dict(os.environ, PATH=str(carpeta) + os.pathsep + os.environ["PATH"])
    proc = subprocess.run(
        ["go", "test", "-count=1", "-json", "-run", "^(" + "|".join(RAICES) + ")$", "./internal/squash/"],
        cwd=REPO, env=env, capture_output=True, text=True, encoding="utf-8", timeout=900,
    )
    accion, medida = {}, {}
    for linea in proc.stdout.splitlines():
        try:
            ev = json.loads(linea)
        except json.JSONDecodeError:
            continue
        test = ev.get("Test")
        if not test:
            continue
        if ev["Action"] in ("pass", "fail", "skip"):
            accion[test] = ev["Action"]
        elif ev["Action"] == "output" and test not in medida and (m := MEDIDA.search(ev.get("Output", ""))):
            medida[test] = f"{m.group(2)} dB" if m.group(1) else f"SSIM {m.group(4)}"
    # Un subtest que no corrió hereda lo de su test: si el test entero se salteó
    # (un ffmpeg sin libx264, por ejemplo), sus casos figuran salteados.
    return {t: (accion.get(t) or accion.get(t.split("/")[0], "—"), medida.get(t, "")) for _, t in TESTS}


def celda(resultado: tuple[str, str]) -> str:
    accion, medida = resultado
    marca, color = {"pass": ("✓", SAGE), "fail": ("✗", ROSE), "skip": ("·", CREAM)}.get(accion, ("?", SUBTLE))
    texto = f"{marca} {medida or ('salteado' if accion == 'skip' else '')}".rstrip()
    return pintar(color, f"{texto:<{ANCHO_CELDA}}")


def main() -> int:
    carpetas = [Path(a).resolve() for a in sys.argv[1:]]
    if not carpetas:
        print("uso: python internal\\squash\\_oracle\\versiones.py <carpeta de ffmpeg> [<carpeta> ...]")
        return 2
    for c in carpetas:
        if not (c / "ffmpeg.exe").exists() and not (c / "ffmpeg").exists():
            print(pintar(ROSE, f"✗ {c} no tiene ffmpeg"))
            return 2

    print()
    print("  " + pintar(LAVENDER, "vidsquash · HDR") + pintar(SUBTLE, "  ·  los tests de internal/squash contra cada ffmpeg"))
    print()
    columnas, t0, consola = [], time.perf_counter(), sys.stdout.isatty()
    for c in carpetas:
        v = version_de(c)
        if consola:
            print(pintar(SUBTLE, f"  corriendo con ffmpeg {v}…"), end="\r", flush=True)
        columnas.append((v, correr(c)))
    if consola:
        print(" " * 60, end="\r")

    print("  " + " " * ANCHO_ETIQUETA + "".join(pintar(SUBTLE, f"{v:<{ANCHO_CELDA}} ") for v, _ in columnas))
    for etiqueta, test in TESTS:
        print("  " + pintar(TEXT, f"{etiqueta:<{ANCHO_ETIQUETA}}") + " ".join(celda(r[test]) for _, r in columnas))

    fallas = sum(r[t][0] == "fail" for _, r in columnas for _, t in TESTS)
    salteados = sum(r[t][0] == "skip" for _, r in columnas for _, t in TESTS)
    total = len(columnas) * len(TESTS)
    print()
    print("  " + pintar(LAVENDER, "●") + pintar(TEXT, f" {len(columnas)} versiones · {total} casos") + pintar(SUBTLE, f" · {time.perf_counter() - t0:.0f} s"))
    print("  " + pintar(ROSE if fallas else SAGE, "●") + pintar(TEXT, f" {fallas} fallas") + pintar(SUBTLE, f" · {salteados} salteados (ffmpeg sin lo necesario)"))
    print()
    return 1 if fallas else 0


if __name__ == "__main__":
    sys.exit(main())
