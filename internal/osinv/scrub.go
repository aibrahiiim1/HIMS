package osinv

import (
	"reflect"
	"strings"
	"unicode/utf8"
)

// scrubReport strips NUL bytes and coerces invalid UTF-8 to valid UTF-8 across
// EVERY string field of a collected Report — recursively through nested structs,
// slices/arrays, and string-valued maps. Windows WMI/registry/WinRM values
// occasionally carry an embedded 0x00 or a non-UTF-8 byte (e.g. a truncated
// REG_SZ or an OEM-codepage string). Postgres text/jsonb columns reject those
// with SQLSTATE 22021 ("invalid byte sequence for encoding \"UTF8\": 0x00"),
// which fails the entire Persist transaction and SILENTLY loses an otherwise
// successful collection — leaving the device unmanaged. Called at the top of
// Persist so it protects every collection path (direct WinRM, native collector,
// WMI, relay agent).
func scrubReport(rep *Report) { scrubValue(reflect.ValueOf(rep).Elem()) }

// cleanString removes NUL bytes and replaces any invalid UTF-8 so the result is
// always a valid, NUL-free Postgres string.
func cleanString(s string) string {
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	return s
}

func needsClean(s string) bool { return strings.IndexByte(s, 0) >= 0 || !utf8.ValidString(s) }

func scrubValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() && needsClean(v.String()) {
			v.SetString(cleanString(v.String()))
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			scrubValue(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanSet() {
				scrubValue(f)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			scrubValue(v.Index(i))
		}
	case reflect.Map:
		// Map values are not addressable, so rebuild any string-valued entries.
		for _, k := range v.MapKeys() {
			if mv := v.MapIndex(k); mv.Kind() == reflect.String && needsClean(mv.String()) {
				v.SetMapIndex(k, reflect.ValueOf(cleanString(mv.String())))
			}
		}
	}
}
