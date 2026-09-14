package network

import (
	"strings"
	"testing"
	"unicode"
)

func TestSanitizeRemoteMessageEliminaSecuenciasDeTerminal(t *testing.T) {
	tests := []struct {
		nombre  string
		entrada string
		salida  string
	}{
		{
			nombre:  "OSC 52 portapapeles",
			entrada: "[Mallory]: \x1b]52;c;R0hPU1RDSEFULVBPSVNPTkVE\x07hola",
			salida:  "[Mallory]: hola",
		},
		{
			nombre:  "CSI color y borrado",
			entrada: "antes\x1b[31mrojo\x1b[0m\x1b[2Jdespués",
			salida:  "antesrojodespués",
		},
		{
			nombre:  "inyección de líneas",
			entrada: "uno\n[Sistema] falso\r\ndos\ttres",
			salida:  "uno [Sistema] falso  dos tres",
		},
		{
			nombre:  "control bidireccional",
			entrada: "abc\u202Etxt.exe",
			salida:  "abctxt.exe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.nombre, func(t *testing.T) {
			if got := sanitizeRemoteMessage(tt.entrada); got != tt.salida {
				t.Fatalf("sanitizeRemoteMessage() = %q, se esperaba %q", got, tt.salida)
			}
		})
	}
}

func TestSanitizeRemoteMessageNoDejaControles(t *testing.T) {
	got := sanitizeRemoteMessage("A\x00B\x1b[5nC\x7fD\u2066E")
	for _, r := range got {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			t.Fatalf("quedó el carácter de control U+%04X en %q", r, got)
		}
	}
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("quedó ESC en %q", got)
	}
}
