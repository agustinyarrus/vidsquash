package fsx

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestNaturalLess(t *testing.T) {
	casos := []struct {
		a, b string
		want bool
	}{
		{"foto2", "foto10", true},
		{"foto10", "foto2", false},
		{"Foto2", "foto10", true}, // sin distinguir mayúsculas
		{"img7", "img007", true},  // mismo valor: menos ceros a la izquierda primero
		{"img007", "img7", false},
		{"x", "x1", true},
		{"a", "b", true},
		{"pag99999999999999999999", "pag100000000000000000000", true}, // sin límite de dígitos
	}
	for _, c := range casos {
		if got := NaturalLess(c.a, c.b); got != c.want {
			t.Errorf("NaturalLess(%q, %q) = %v, esperaba %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSortNatural(t *testing.T) {
	got := []string{"pag10.png", "pag2.png", "pag1.png", "Pag3.png"}
	SortNatural(got)
	want := []string{"pag1.png", "pag2.png", "Pag3.png", "pag10.png"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SortNatural = %v, esperaba %v", got, want)
	}
}

// arbol arma una carpeta de prueba con archivos, una subcarpeta y una oculta.
func arbol(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"a.webp", "b.WEBP", "c.png", "sub/d.webp", "sub/deep/e.webp", ".oculta/f.webp"} {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func names(dir string, files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		rel, _ := filepath.Rel(dir, f)
		out[i] = filepath.ToSlash(rel)
	}
	return out
}

func webp(p string) bool { return strings.EqualFold(filepath.Ext(p), ".webp") }

func TestExpandGlob(t *testing.T) {
	dir := arbol(t)
	files, probs := Expand([]string{dir + "/*.webp"}, ExpandOptions{})
	want := []string{"a.webp"}
	if runtime.GOOS == "windows" {
		want = append(want, "b.WEBP") // el sistema de archivos no distingue mayúsculas
	}
	if len(probs) != 0 || !reflect.DeepEqual(names(dir, files), want) {
		t.Errorf("*.webp → %v (problemas %v), esperaba %v", names(dir, files), probs, want)
	}
}

func TestExpandDobleAsterisco(t *testing.T) {
	dir := arbol(t)
	files, _ := Expand([]string{dir + "/**/*.webp"}, ExpandOptions{})
	got := names(dir, files)
	for _, must := range []string{"a.webp", "sub/d.webp", "sub/deep/e.webp"} {
		if !contains(got, must) {
			t.Errorf("**/*.webp no encontró %s: %v", must, got)
		}
	}
	if contains(got, ".oculta/f.webp") {
		t.Errorf("** no debería entrar en carpetas ocultas: %v", got)
	}
}

func TestExpandCarpeta(t *testing.T) {
	dir := arbol(t)
	plano, _ := Expand([]string{dir}, ExpandOptions{Accept: webp})
	if contains(names(dir, plano), "sub/d.webp") {
		t.Errorf("sin -r no debería entrar en subcarpetas: %v", names(dir, plano))
	}
	rec, _ := Expand([]string{dir}, ExpandOptions{Accept: webp, Recursive: true})
	got := names(dir, rec)
	if !contains(got, "sub/deep/e.webp") || contains(got, "c.png") || contains(got, ".oculta/f.webp") {
		t.Errorf("con -r: %v", got)
	}
}

func TestExpandSinDuplicados(t *testing.T) {
	dir := arbol(t)
	a := filepath.Join(dir, "a.webp")
	files, _ := Expand([]string{a, a, dir + "/a.webp"}, ExpandOptions{})
	if len(files) != 1 {
		t.Errorf("el mismo archivo por tres caminos tendría que aparecer una vez: %v", files)
	}
}

func TestExpandSugiereElParecido(t *testing.T) {
	dir := arbol(t)
	_, probs := Expand([]string{filepath.Join(dir, "a.webpp")}, ExpandOptions{})
	if len(probs) != 1 || !strings.Contains(probs[0].Err.Error(), "¿quisiste decir a.webp?") {
		t.Errorf("problemas = %v, esperaba la sugerencia de a.webp", probs)
	}
}

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "salida.txt")
	if err := WriteAtomic(dst, func(w io.Writer) error { _, err := io.WriteString(w, "hola"); return err }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "hola" {
		t.Errorf("contenido = %q", b)
	}
	// Si la escritura falla, no queda ni el archivo nuevo ni el temporal, y el
	// anterior sigue intacto.
	boom := errors.New("falla a propósito")
	err := WriteAtomic(dst, func(w io.Writer) error { io.WriteString(w, "a medias"); return boom })
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, esperaba el error de la escritura", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "hola" {
		t.Errorf("un fallo pisó el archivo anterior: %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("quedaron temporales: %v", entries)
	}
}

func TestSameFileYReplaceExt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	os.WriteFile(p, nil, 0o644)
	if !SameFile(p, filepath.Join(dir, ".", "x.txt")) {
		t.Error("SameFile tendría que reconocer el mismo archivo por otra ruta")
	}
	if SameFile(p, filepath.Join(dir, "no-existe.txt")) {
		t.Error("SameFile con un archivo inexistente tendría que ser false")
	}
	if got := ReplaceExt(`C:\fotos\gato.webp`, ".png"); got != `C:\fotos\gato.png` {
		t.Errorf("ReplaceExt = %q", got)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
