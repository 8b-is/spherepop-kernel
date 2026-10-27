package strict

import (
	"errors"
	"testing"
)

type inner struct {
	N uint8 `json:"n"`
}

type doc struct {
	A string              `json:"a"`
	B []inner             `json:"b"`
	C map[string][]string `json:"c,omitempty"`
	D bool                `json:"d,omitempty"`
	I int32               `json:"i"`
}

func TestUnmarshalRules(t *testing.T) {
	ok := `{"a":"x","b":[{"n":3}],"i":-4}`
	var d doc
	if err := Unmarshal([]byte(ok), &d); err != nil || d.A != "x" || d.B[0].N != 3 || d.I != -4 {
		t.Fatalf("valid document rejected: %v %+v", err, d)
	}
	if err := Unmarshal([]byte(`{"a":"x","b":[],"i":0,"c":{"k":["v"]},"d":true}`), &doc{}); err != nil {
		t.Fatalf("optional members present with values: %v", err)
	}
	bad := map[string]string{
		"missing":        `{"a":"x","i":1}`,
		"unknown":        `{"a":"x","b":[],"i":1,"z":1}`,
		"case":           `{"A":"x","b":[],"i":1}`,
		"duplicate":      `{"a":"x","a":"y","b":[],"i":1}`,
		"null":           `{"a":null,"b":[],"i":1}`,
		"null array":     `{"a":"x","b":null,"i":1}`,
		"wrong type":     `{"a":1,"b":[],"i":1}`,
		"fraction":       `{"a":"x","b":[],"i":1.0}`,
		"exponent":       `{"a":"x","b":[],"i":1e0}`,
		"overflow":       `{"a":"x","b":[{"n":256}],"i":1}`,
		"negative":       `{"a":"x","b":[{"n":-1}],"i":1}`,
		"zero optional":  `{"a":"x","b":[],"i":1,"d":false}`,
		"empty optional": `{"a":"x","b":[],"i":1,"c":{}}`,
		"trailing":       `{"a":"x","b":[],"i":1} {}`,
		"not object":     `[]`,
		"nested unknown": `{"a":"x","b":[{"n":1,"m":2}],"i":1}`,
	}
	for name, src := range bad {
		if err := Unmarshal([]byte(src), &doc{}); !errors.Is(err, ErrStrict) {
			t.Errorf("%s: accepted or wrong error: %v", name, err)
		}
	}
}
