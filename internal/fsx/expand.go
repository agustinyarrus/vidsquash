// Package fsx resuelve lo que el user escribe en la línea de comandos (archivos,
// carpetas, patrones con * y **) y escribe los resultados de forma atómica.
package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/agustinyarrus/vidsquash/internal/textdist"
)

// ExpandOptions controla cómo se abren las carpetas y los patrones.
type ExpandOptions struct {
	// Recursive recorre las subcarpetas de las carpetas nombradas.
	Recursive bool
	// Accept filtra los archivos DESCUBIERTOS (dentro de carpetas o por
	// patrón). Los archivos nombrados explícitamente pasan siempre: si el user
	// lo escribió, lo quiere.
	Accept func(path string) bool
}

// Problem es un patrón que no aportó nada, con el porqué.
type Problem struct {
	Pattern string
	Err     error
}

func (p Problem) Error() string { return p.Pattern + ": " + p.Err.Error() }

// Expand convierte patrones en una lista de archivos sin duplicados, en el
// orden de los patrones y, dentro de cada uno, en orden natural (foto2 antes
// que foto10). En Windows ni cmd ni PowerShell expanden los comodines para un
// exe nativo: lo hacemos acá, con ** incluido.
func Expand(patterns []string, opt ExpandOptions) ([]string, []Problem) {
	var files []string
	var problems []Problem
	seen := map[string]bool{}
	add := func(p string) {
		key := dedupeKey(p)
		if !seen[key] {
			seen[key] = true
			files = append(files, p)
		}
	}
	for _, pat := range patterns {
		if pat == "-" {
			add(pat)
			continue
		}
		found, err := expandOne(pat, opt)
		if err != nil {
			problems = append(problems, Problem{pat, err})
			continue
		}
		if len(found) == 0 {
			problems = append(problems, Problem{pat, errors.New("no coincide con ningún archivo")})
			continue
		}
		for _, f := range found {
			add(f)
		}
	}
	return files, problems
}

func expandOne(pat string, opt ExpandOptions) ([]string, error) {
	if !hasMeta(pat) {
		st, err := os.Stat(pat)
		if err != nil {
			return nil, describeMissing(pat, err)
		}
		if !st.IsDir() {
			return []string{pat}, nil
		}
		return listDir(pat, opt)
	}
	matches, err := glob(pat)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range matches {
		st, err := os.Stat(m)
		switch {
		case err != nil:
			continue
		case st.IsDir():
			if opt.Recursive {
				sub, _ := listDir(m, opt)
				out = append(out, sub...)
			}
		case opt.Accept == nil || opt.Accept(m):
			out = append(out, m)
		}
	}
	return out, nil
}

// listDir devuelve los archivos aceptados de dir (recursivo si se pidió).
func listDir(dir string, opt ExpandOptions) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil // una subcarpeta sin permiso no frena el resto
		}
		if d.IsDir() {
			if p != dir && (!opt.Recursive || isHiddenName(d.Name())) {
				return filepath.SkipDir
			}
			return nil
		}
		if opt.Accept == nil || opt.Accept(p) {
			out = append(out, p)
		}
		return nil
	})
	SortNatural(out)
	return out, err
}

func hasMeta(s string) bool { return strings.ContainsAny(s, "*?[") }

func isHiddenName(name string) bool { return strings.HasPrefix(name, ".") }

// glob resuelve un patrón segmento a segmento; "**" equivale a cero o más
// carpetas. Sin distinguir mayúsculas en Windows, como el sistema de archivos.
func glob(pattern string) ([]string, error) {
	pattern = filepath.FromSlash(strings.ReplaceAll(pattern, "/", string(filepath.Separator)))
	vol := filepath.VolumeName(pattern)
	rest := pattern[len(vol):]
	base := vol
	if strings.HasPrefix(rest, string(filepath.Separator)) {
		base += string(filepath.Separator)
	}
	var segs []string
	for _, s := range strings.Split(rest, string(filepath.Separator)) {
		if s != "" {
			segs = append(segs, s)
		}
	}
	for _, s := range segs {
		if _, err := filepath.Match(foldCase(s), "x"); err != nil && s != "**" {
			return nil, fmt.Errorf("patrón inválido %q", s)
		}
	}
	i := 0
	for i < len(segs) && !hasMeta(segs[i]) {
		base = joinPath(base, segs[i])
		i++
	}
	if base == "" {
		base = "."
	}
	var out []string
	walkGlob(base, segs[i:], &out)
	SortNatural(out)
	return out, nil
}

func joinPath(base, seg string) string {
	if base == "" {
		return seg
	}
	if strings.HasSuffix(base, string(filepath.Separator)) || strings.HasSuffix(base, ":") {
		return base + seg
	}
	return base + string(filepath.Separator) + seg
}

func walkGlob(dir string, segs []string, out *[]string) {
	if len(segs) == 0 {
		*out = append(*out, trimDot(dir))
		return
	}
	seg := segs[0]
	if seg == "**" {
		walkGlob(dir, segs[1:], out) // ** = cero carpetas…
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() && !isHiddenName(e.Name()) {
				walkGlob(joinPath(dir, e.Name()), segs, out) // …o una más, con ** todavía vivo
			}
		}
		return
	}
	if !hasMeta(seg) {
		p := joinPath(dir, seg)
		if _, err := os.Stat(p); err == nil {
			walkGlob(p, segs[1:], out)
		}
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	pat := foldCase(seg)
	for _, e := range entries {
		ok, _ := filepath.Match(pat, foldCase(e.Name()))
		if !ok {
			continue
		}
		p := joinPath(dir, e.Name())
		if len(segs) == 1 {
			*out = append(*out, trimDot(p))
		} else if e.IsDir() {
			walkGlob(p, segs[1:], out)
		}
	}
}

func trimDot(p string) string {
	return strings.TrimPrefix(p, "."+string(filepath.Separator))
}

func foldCase(s string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(s)
	}
	return s
}

func dedupeKey(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return foldCase(filepath.Clean(p))
}

// describeMissing explica por qué un archivo no está y sugiere el vecino más
// parecido de la misma carpeta ("¿quisiste decir foto-perfil.webp?").
func describeMissing(pat string, err error) error {
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	dir, name := filepath.Split(pat)
	if dir == "" {
		dir = "."
	}
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		return errors.New("no existe")
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if s := textdist.Closest(name, names); s != "" {
		return fmt.Errorf("no existe; ¿quisiste decir %s?", s)
	}
	return errors.New("no existe")
}

// SortNatural ordena como un humano: sin distinguir mayúsculas y comparando
// los tramos de dígitos por su valor numérico. O(n log n) comparaciones.
func SortNatural(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool { return NaturalLess(paths[i], paths[j]) })
}

// NaturalLess compara a y b por tramos: texto contra texto sin mayúsculas,
// números contra números por valor (sin límite de dígitos: "007" = "7", y a
// igualdad gana el de menos ceros a la izquierda para que el orden sea total).
func NaturalLess(a, b string) bool {
	ia, ib := 0, 0
	for ia < len(a) && ib < len(b) {
		ca, cb := a[ia], b[ib]
		if isDigit(ca) && isDigit(cb) {
			ja, jb := ia, ib
			for ja < len(a) && isDigit(a[ja]) {
				ja++
			}
			for jb < len(b) && isDigit(b[jb]) {
				jb++
			}
			na, nb := strings.TrimLeft(a[ia:ja], "0"), strings.TrimLeft(b[ib:jb], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			if ja-ia != jb-ib {
				return ja-ia < jb-ib
			}
			ia, ib = ja, jb
			continue
		}
		la, lb := lower(ca), lower(cb)
		if la != lb {
			return la < lb
		}
		ia++
		ib++
	}
	if len(a)-ia != len(b)-ib {
		return len(a)-ia < len(b)-ib
	}
	return a < b
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
