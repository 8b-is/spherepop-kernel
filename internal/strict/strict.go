// Package strict decodes JSON documents under the rules of SPEC section 18.9.
// The standard decoder is lenient in ways an interoperable evidence format
// cannot afford: it matches member names without regard to case, accepts
// missing members, accepts null for any type, and keeps the last of a
// duplicated member. This package checks the document against the Go type
// first and only then hands it to the standard decoder, so a document that
// passes means the same thing to every conforming reader.
package strict

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// ErrStrict reports a document that breaks the strictness rules.
var ErrStrict = errors.New("strict: document is not canonical JSON for this type")

func fail(path, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrStrict, path, fmt.Sprintf(format, args...))
}

// Unmarshal checks b against the type of v, which must be a non-nil pointer,
// and then decodes it into v.
//
// Every member of a struct must appear exactly once under its exact name,
// unless its tag says omitempty, in which case it may be absent but must not
// be present with its zero value. No value may be null. Integers must be
// written without fraction or exponent and fit the Go type.
func Unmarshal(b []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("strict: Unmarshal needs a non-nil pointer")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if _, err := check(dec, rv.Elem().Type(), "$"); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fail("$", "trailing data after the document")
	}
	return json.Unmarshal(b, v)
}

type field struct {
	name      string
	omitempty bool
	typ       reflect.Type
}

func fields(t reflect.Type) []field {
	var out []field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" || !f.IsExported() {
			continue
		}
		parts := strings.Split(tag, ",")
		name := parts[0]
		if name == "" {
			name = f.Name
		}
		fd := field{name: name, typ: f.Type}
		for _, o := range parts[1:] {
			if o == "omitempty" {
				fd.omitempty = true
			}
		}
		out = append(out, fd)
	}
	return out
}

// check reads one value of type t from dec. It reports whether the value is
// the zero value of its type, which matters only for omitempty members.
func check(dec *json.Decoder, t reflect.Type, path string) (zero bool, err error) {
	tok, err := dec.Token()
	if err != nil {
		return false, fail(path, "%v", err)
	}
	if tok == nil {
		return false, fail(path, "null is not allowed")
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		s, ok := tok.(string)
		if !ok {
			return false, fail(path, "want a string")
		}
		return s == "", nil
	case reflect.Bool:
		b, ok := tok.(bool)
		if !ok {
			return false, fail(path, "want a boolean")
		}
		return !b, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, ok := tok.(json.Number)
		if !ok {
			return false, fail(path, "want an integer")
		}
		txt := n.String()
		if strings.ContainsAny(txt, ".eE") {
			return false, fail(path, "integer %q has a fraction or exponent", txt)
		}
		if t.Kind() >= reflect.Int && t.Kind() <= reflect.Int64 {
			x, err := strconv.ParseInt(txt, 10, t.Bits())
			if err != nil {
				return false, fail(path, "integer %q out of range", txt)
			}
			return x == 0, nil
		}
		x, err := strconv.ParseUint(txt, 10, t.Bits())
		if err != nil {
			return false, fail(path, "integer %q out of range", txt)
		}
		return x == 0, nil
	case reflect.Slice:
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return false, fail(path, "want an array")
		}
		n := 0
		for dec.More() {
			if _, err := check(dec, t.Elem(), fmt.Sprintf("%s[%d]", path, n)); err != nil {
				return false, err
			}
			n++
		}
		if _, err := dec.Token(); err != nil {
			return false, fail(path, "%v", err)
		}
		return n == 0, nil
	case reflect.Map:
		if d, ok := tok.(json.Delim); !ok || d != '{' {
			return false, fail(path, "want an object")
		}
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return false, fail(path, "%v", err)
			}
			k, _ := kt.(string)
			if seen[k] {
				return false, fail(path, "duplicate member %q", k)
			}
			seen[k] = true
			if _, err := check(dec, t.Elem(), path+"."+k); err != nil {
				return false, err
			}
		}
		if _, err := dec.Token(); err != nil {
			return false, fail(path, "%v", err)
		}
		return len(seen) == 0, nil
	case reflect.Struct:
		if d, ok := tok.(json.Delim); !ok || d != '{' {
			return false, fail(path, "want an object")
		}
		fs := fields(t)
		byName := map[string]field{}
		for _, f := range fs {
			byName[f.name] = f
		}
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return false, fail(path, "%v", err)
			}
			k, _ := kt.(string)
			f, ok := byName[k]
			if !ok {
				return false, fail(path, "unknown member %q", k)
			}
			if seen[k] {
				return false, fail(path, "duplicate member %q", k)
			}
			seen[k] = true
			z, err := check(dec, f.typ, path+"."+k)
			if err != nil {
				return false, err
			}
			if z && f.omitempty {
				return false, fail(path, "member %q is optional and present with its zero value", k)
			}
		}
		if _, err := dec.Token(); err != nil {
			return false, fail(path, "%v", err)
		}
		for _, f := range fs {
			if !f.omitempty && !seen[f.name] {
				return false, fail(path, "missing member %q", f.name)
			}
		}
		return false, nil
	}
	return false, fail(path, "unsupported type %s", t)
}
