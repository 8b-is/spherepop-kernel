package vectors

import "testing"

func TestVectors(t *testing.T) {
	for _, v := range All() {
		t.Run(v.Name, func(t *testing.T) {
			if err := v.Run(); err != nil {
				t.Fatalf("[%s] %v", v.Ref, err)
			}
		})
	}
}
