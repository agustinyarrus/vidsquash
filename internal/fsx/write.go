package fsx

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const writeBuffer = 1 << 20 // 1 MiB: pocas llamadas al sistema aun en archivos grandes

// WriteAtomic escribe path de forma atómica: todo va a un temporal en la MISMA
// carpeta (el rename entre volúmenes no es atómico) y recién al final se
// renombra. Si write falla, o llega un Ctrl+C, no queda un archivo a medias ni
// se pisa el anterior.
//
// No hace fsync a propósito: protege contra cortes del programa, que es lo que
// pasa de verdad; contra un corte de luz costaría un FlushFileBuffers por
// archivo, caro en una tanda de cientos de imágenes.
func WriteAtomic(path string, write func(w io.Writer) error) (err error) {
	if path == "-" {
		bw := bufio.NewWriterSize(os.Stdout, writeBuffer)
		if err := write(bw); err != nil {
			return err
		}
		return bw.Flush()
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("no puedo crear el temporal en %s: %w", dir, err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	bw := bufio.NewWriterSize(f, writeBuffer)
	if err = write(bw); err != nil {
		return err
	}
	if err = bw.Flush(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return renameWithRetry(tmp, path)
}

// renameWithRetry reintenta con espera exponencial: en Windows un visor o el
// antivirus pueden tener el destino abierto unos milisegundos.
func renameWithRetry(from, to string) error {
	delay := 40 * time.Millisecond
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(delay)
		delay *= 2
	}
	os.Remove(from)
	return fmt.Errorf("no puedo reemplazar %s (¿está abierto en otro programa?): %w", filepath.Base(to), err)
}

// SameFile dice si a y b son el mismo archivo en disco (mismo ID de volumen e
// índice, aunque se escriban distinto o haya enlaces duros).
func SameFile(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

// Exists dice si hay algo en path.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

// Display acorta una ruta para mostrarla: relativa si cuelga de la carpeta
// actual, absoluta si no.
func Display(path string) string {
	if path == "-" {
		return "(entrada estándar)"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	wd, err := os.Getwd()
	if err != nil {
		return abs
	}
	rel, err := filepath.Rel(wd, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return abs
	}
	return rel
}

// ReplaceExt cambia la extensión: foto.webp → foto.png.
func ReplaceExt(path, ext string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + ext
}
