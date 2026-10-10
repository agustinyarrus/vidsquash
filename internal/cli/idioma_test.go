package cli

import (
	"strings"
	"testing"

	"github.com/agustinyarrus/vidsquash/internal/i18n"
	"github.com/agustinyarrus/vidsquash/internal/tui"
)

// enIdioma corre la prueba en un idioma y deja el idioma fuente al terminar.
func enIdioma(t *testing.T, l i18n.Lang) {
	t.Helper()
	i18n.Set(l)
	t.Cleanup(func() { i18n.Set(i18n.ES) })
}

func TestLangEnv(t *testing.T) {
	for in, want := range map[string]string{"killport": "KILLPORT_LANG", "pdf-merge": "PDF_MERGE_LANG", "clip2qr": "CLIP2QR_LANG"} {
		if got := LangEnv(in); got != want {
			t.Errorf("LangEnv(%q) = %q, quería %q", in, got, want)
		}
	}
}

// ResolveLang mira --lang antes de armar la App: con =, separado, después de otros flags y
// nunca después de "--" (de ahí en adelante son posicionales del usuario).
func TestResolveLangLeeElFlag(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.ES) })
	t.Setenv("PRUEBA_LANG", "")
	casos := []struct {
		args []string
		want i18n.Lang
		src  i18n.Source
	}{
		{[]string{"--lang", "en"}, i18n.EN, i18n.FromFlag},
		{[]string{"-f", "--lang=en", "a.txt"}, i18n.EN, i18n.FromFlag},
		{[]string{"--lang=es"}, i18n.ES, i18n.FromFlag},
		{[]string{"--lang", "English"}, i18n.EN, i18n.FromFlag},
	}
	for _, c := range casos {
		got, src := ResolveLang("prueba", c.args)
		if got != c.want || src != c.src || i18n.Current() != c.want {
			t.Errorf("%v: %v (%v), quería %v (%v)", c.args, got, src, c.want, c.src)
		}
	}
	if _, src := ResolveLang("prueba", []string{"--", "--lang", "en"}); src == i18n.FromFlag {
		t.Error(`después de "--" no hay flags`)
	}
	t.Setenv("PRUEBA_LANG", "en")
	if got, src := ResolveLang("prueba", nil); got != i18n.EN || src != i18n.FromAppEnv {
		t.Errorf("PRUEBA_LANG=en: %v (%v)", got, src)
	}
	if got, src := ResolveLang("prueba", []string{"--lang", "es"}); got != i18n.ES || src != i18n.FromFlag {
		t.Errorf("--lang le gana a la variable: %v (%v)", got, src)
	}
}

// Parse consume --lang (no es un posicional) y rechaza un idioma que no existe, en el idioma actual.
func TestParseLang(t *testing.T) {
	a, _ := newApp()
	pos, err := a.Parse([]string{"x.txt", "--lang", "en", "--lang=auto", "y.txt"})
	if err != nil || strings.Join(pos, ",") != "x.txt,y.txt" {
		t.Fatalf("pos=%v err=%v", pos, err)
	}
	if _, err := a.Parse([]string{"--lang"}); err == nil {
		t.Error("--lang sin valor tiene que fallar")
	}
	enIdioma(t, i18n.ES)
	if _, err := a.Parse([]string{"--lang", "fr"}); err == nil || !strings.Contains(err.Error(), "no es un idioma") {
		t.Errorf("castellano: %v", err)
	}
	enIdioma(t, i18n.EN)
	if _, err := a.Parse([]string{"--lang", "fr"}); err == nil || !strings.Contains(err.Error(), "is not a language") {
		t.Errorf("inglés: %v", err)
	}
	if _, err := a.Parse([]string{"--colour"}); err == nil || !strings.Contains(err.Error(), "unknown --colour; did you mean --color") &&
		!strings.Contains(err.Error(), "unknown --colour") {
		t.Errorf("flag desconocido en inglés: %v", err)
	}
}

// La ayuda: títulos y flags generales en el idioma de la salida, y --lang con la variable propia.
func TestAyudaEnLosDosIdiomas(t *testing.T) {
	for _, c := range []struct {
		l     i18n.Lang
		hay   []string
		nohay []string
	}{
		{i18n.ES, []string{"uso", "general", "muestra esta ayuda", "--lang", "PRUEBA_LANG"}, []string{"usage", "show this help"}},
		{i18n.EN, []string{"usage", "general", "show this help", "--lang", "PRUEBA_LANG", "output language"}, []string{"uso", "muestra esta ayuda"}},
	} {
		enIdioma(t, c.l)
		a, _ := newApp()
		a.Usage = []string{"prueba algo"}
		term := tui.Open()
		term.SetWidth(140)
		texto := strings.Join(a.Help(term), "\n")
		for _, s := range c.hay {
			if !strings.Contains(texto, s) {
				t.Errorf("%v: falta %q en la ayuda", c.l, s)
			}
		}
		for _, s := range c.nohay {
			if strings.Contains(texto, s) {
				t.Errorf("%v: sobra %q en la ayuda", c.l, s)
			}
		}
	}
}
